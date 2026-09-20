// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const serviceAccountKeychainService = "1password-pp-cli/service-account"

type serviceAccountMetadata struct {
	Name         string     `json:"name"`
	Account      string     `json:"account"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastVerified *time.Time `json:"last_verified,omitempty"`
}

type serviceAccountStoreFile struct {
	ServiceAccounts map[string]serviceAccountMetadata `json:"service_accounts"`
}

type secretStore interface {
	Set(context.Context, string, string) error
	Get(context.Context, string) (string, error)
	Delete(context.Context, string) error
}

type opAuthContextKey struct{}

type opAuthContext struct {
	mode           string
	serviceAccount string
	accountHint    string
	tokenSource    string
	token          string
	explicitToken  bool
	opAccount      string
}

func resolveOpAuth(ctx context.Context, flags *rootFlags) (opAuthContext, error) {
	if flags.opServiceAccount != "" && flags.opServiceAccountTokenEnv != "" {
		return opAuthContext{}, errors.New("--op-service-account and --op-service-account-token-env are mutually exclusive")
	}
	if flags.opServiceAccount != "" {
		if conflicts := connectEnvConflicts(); len(conflicts) > 0 {
			return opAuthContext{}, fmt.Errorf("selected service account cannot be used while Connect environment is set; unset %s", strings.Join(conflicts, ", "))
		}
		meta, _, err := getServiceAccountMetadata(flags.opServiceAccount)
		if err != nil {
			return opAuthContext{}, err
		}
		token, err := serviceAccountSecrets.Get(ctx, flags.opServiceAccount)
		if err != nil {
			return opAuthContext{}, err
		}
		return opAuthContext{mode: "service-account-profile", serviceAccount: meta.Name, accountHint: meta.Account, tokenSource: "keychain", token: token, explicitToken: true}, nil
	}
	if flags.opServiceAccountTokenEnv != "" {
		if conflicts := connectEnvConflicts(); len(conflicts) > 0 {
			return opAuthContext{}, fmt.Errorf("selected service account cannot be used while Connect environment is set; unset %s", strings.Join(conflicts, ", "))
		}
		token, ok := os.LookupEnv(flags.opServiceAccountTokenEnv)
		if !ok || strings.TrimSpace(token) == "" {
			return opAuthContext{}, fmt.Errorf("service-account token environment variable %q is missing or empty", flags.opServiceAccountTokenEnv)
		}
		return opAuthContext{mode: "service-account-env", tokenSource: "env:" + flags.opServiceAccountTokenEnv, token: token, explicitToken: true}, nil
	}
	if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" {
		return opAuthContext{mode: "service-account", tokenSource: "ambient", token: os.Getenv("OP_SERVICE_ACCOUNT_TOKEN")}, nil
	}
	if flags.opAccount != "" || os.Getenv("OP_ACCOUNT") != "" {
		return opAuthContext{mode: "desktop-or-session", tokenSource: "ambient", opAccount: flags.opAccount}, nil
	}
	return opAuthContext{mode: "op-default", tokenSource: "ambient", opAccount: flags.opAccount}, nil
}

func opAuthFromContext(ctx context.Context) opAuthContext {
	if auth, ok := ctx.Value(opAuthContextKey{}).(opAuthContext); ok {
		return auth
	}
	auth, _ := resolveOpAuth(ctx, &rootFlags{})
	return auth
}

func (a opAuthContext) childEnv() []string {
	remove := map[string]bool{"OP_SERVICE_ACCOUNT_TOKEN": true}
	if strings.HasPrefix(a.tokenSource, "env:") {
		remove[strings.TrimPrefix(a.tokenSource, "env:")] = true
	}
	out := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		name := item
		if i := strings.IndexByte(item, '='); i >= 0 {
			name = item[:i]
		}
		if !remove[name] {
			out = append(out, item)
		}
	}
	if a.token != "" {
		out = append(out, "OP_SERVICE_ACCOUNT_TOKEN="+a.token)
	}
	if a.opAccount != "" {
		out = append(out, "OP_ACCOUNT="+a.opAccount)
	}
	return out
}

func (a opAuthContext) redact(data []byte) []byte {
	if a.token == "" {
		return data
	}
	return []byte(strings.ReplaceAll(string(data), a.token, "[REDACTED]"))
}

type keychainSecretStore struct{}

func (keychainSecretStore) Set(ctx context.Context, name, token string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("named service accounts require macOS Keychain; use --op-service-account-token-env on this platform")
	}
	return setKeychainSecret(ctx, serviceAccountKeychainService, name, token)
}

func (keychainSecretStore) Get(ctx context.Context, name string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("named service accounts require macOS Keychain; use --op-service-account-token-env on this platform")
	}
	token, err := getKeychainSecret(ctx, serviceAccountKeychainService, name)
	if err != nil {
		return "", fmt.Errorf("service-account token %q is unavailable: %w", name, err)
	}
	if token == "" {
		return "", fmt.Errorf("service-account token %q is empty in Keychain", name)
	}
	return token, nil
}

func (keychainSecretStore) Delete(ctx context.Context, name string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("named service accounts require macOS Keychain")
	}
	if err := deleteKeychainSecret(ctx, serviceAccountKeychainService, name); err != nil {
		return fmt.Errorf("removing service-account token %q from Keychain: %w", name, err)
	}
	return nil
}

var serviceAccountSecrets secretStore = keychainSecretStore{}

func serviceAccountStorePath() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	dir = filepath.Join(dir, ".1password-pp-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating service-account config directory: %w", err)
	}
	return filepath.Join(dir, "service-accounts.json"), nil
}

func loadServiceAccountStore() (*serviceAccountStoreFile, error) {
	p, err := serviceAccountStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &serviceAccountStoreFile{ServiceAccounts: map[string]serviceAccountMetadata{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading service-account metadata: %w", err)
	}
	var store serviceAccountStoreFile
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parsing service-account metadata: %w", err)
	}
	if store.ServiceAccounts == nil {
		store.ServiceAccounts = map[string]serviceAccountMetadata{}
	}
	return &store, nil
}

func saveServiceAccountStore(store *serviceAccountStoreFile) error {
	p, err := serviceAccountStorePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling service-account metadata: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing service-account metadata: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("securing service-account metadata: %w", err)
	}
	return os.Rename(tmp, p)
}

func validServiceAccountName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func readServiceAccountToken(cmd *cobra.Command, noInput, tokenStdin bool, tokenEnv string) (string, error) {
	if tokenStdin && tokenEnv != "" {
		return "", errors.New("--token-stdin and --token-env are mutually exclusive")
	}
	if tokenEnv != "" {
		token, ok := os.LookupEnv(tokenEnv)
		if !ok || strings.TrimSpace(token) == "" {
			return "", fmt.Errorf("token environment variable %q is missing or empty", tokenEnv)
		}
		return token, nil
	}
	if tokenStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", errors.New("reading service-account token from stdin")
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", errors.New("service-account token from stdin is empty")
		}
		return token, nil
	}
	if noInput {
		return "", errors.New("interactive token prompt disabled; use --token-stdin or --token-env")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("masked token prompt requires a terminal; use --token-stdin or --token-env")
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Service-account token (input hidden): ")
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil || len(bytesTrimSpace(data)) == 0 {
		return "", errors.New("could not read a non-empty service-account token")
	}
	return string(bytesTrimSpace(data)), nil
}

func bytesTrimSpace(v []byte) []byte { return []byte(strings.TrimSpace(string(v))) }

func newServiceAccountsCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "service-accounts", Short: "Manage named 1Password service-account tokens in OS secure storage", RunE: parentNoSubcommandRunE(flags)}
	cmd.AddCommand(newServiceAccountAddCmd(flags), newServiceAccountListCmd(flags), newServiceAccountShowCmd(flags), newServiceAccountDoctorCmd(flags), newServiceAccountRepairAccessCmd(flags), newServiceAccountRemoveCmd(flags))
	return cmd
}

func newServiceAccountAddCmd(flags *rootFlags) *cobra.Command {
	var account, tokenEnv string
	var tokenStdin bool
	cmd := &cobra.Command{
		Use: "add <name>", Short: "Store a named token in OS secure storage and non-secret metadata on disk", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validServiceAccountName(name) {
				return errors.New("service-account name must contain only lowercase letters, digits, hyphens, or underscores")
			}
			if strings.TrimSpace(account) == "" {
				return errors.New("--account is required")
			}
			if flags.dryRun {
				return flags.printJSON(cmd, map[string]any{"dry_run": true, "name": name, "account_hint": strings.TrimSpace(account), "would_store": true})
			}
			store, err := loadServiceAccountStore()
			if err != nil {
				return err
			}
			oldToken := ""
			if _, exists := store.ServiceAccounts[name]; exists {
				oldToken, err = serviceAccountSecrets.Get(cmd.Context(), name)
				if err != nil {
					return fmt.Errorf("cannot preserve existing service-account token %q; replacement aborted: %w", name, err)
				}
			}
			token, err := readServiceAccountToken(cmd, flags.noInput, tokenStdin, tokenEnv)
			if err != nil {
				return err
			}
			defer func() { token = "" }()
			if err := serviceAccountSecrets.Set(cmd.Context(), name, token); err != nil {
				return err
			}
			now := time.Now().UTC()
			created := now
			if old, ok := store.ServiceAccounts[name]; ok {
				created = old.CreatedAt
			}
			store.ServiceAccounts[name] = serviceAccountMetadata{Name: name, Account: strings.TrimSpace(account), CreatedAt: created, UpdatedAt: now}
			if err := saveServiceAccountStore(store); err != nil {
				var rollbackErr error
				if oldToken != "" {
					rollbackErr = serviceAccountSecrets.Set(context.WithoutCancel(cmd.Context()), name, oldToken)
				} else {
					rollbackErr = serviceAccountSecrets.Delete(context.WithoutCancel(cmd.Context()), name)
				}
				if rollbackErr != nil {
					return fmt.Errorf("%w; restoring previous Keychain state also failed: %v", err, rollbackErr)
				}
				return err
			}
			return flags.printJSON(cmd, map[string]any{"name": name, "account_hint": strings.TrimSpace(account), "token_source": "keychain", "stored": true})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "1Password account URL or shorthand (metadata only)")
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read the token from stdin")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "", "Read the token from this environment variable")
	return cmd
}

func sortedServiceAccounts(store *serviceAccountStoreFile) []serviceAccountMetadata {
	out := make([]serviceAccountMetadata, 0, len(store.ServiceAccounts))
	for _, meta := range store.ServiceAccounts {
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func newServiceAccountListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List named service-account metadata (never tokens)", Annotations: map[string]string{"mcp:read-only": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := loadServiceAccountStore()
		if err != nil {
			return err
		}
		return flags.printJSON(cmd, sortedServiceAccounts(store))
	}}
}

func getServiceAccountMetadata(name string) (serviceAccountMetadata, *serviceAccountStoreFile, error) {
	store, err := loadServiceAccountStore()
	if err != nil {
		return serviceAccountMetadata{}, nil, err
	}
	meta, ok := store.ServiceAccounts[name]
	if !ok {
		return serviceAccountMetadata{}, store, fmt.Errorf("named service account %q not found", name)
	}
	return meta, store, nil
}

func newServiceAccountShowCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{Use: "show <name>", Short: "Show named service-account metadata (never the token)", Args: cobra.ExactArgs(1), Annotations: map[string]string{"mcp:read-only": "true"}, RunE: func(cmd *cobra.Command, args []string) error {
		meta, _, err := getServiceAccountMetadata(args[0])
		if err != nil {
			return err
		}
		return flags.printJSON(cmd, meta)
	}}
}

func newServiceAccountDoctorCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{Use: "doctor <name>", Short: "Verify a named service account without revealing its token", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if conflicts := connectEnvConflicts(); len(conflicts) > 0 {
			return fmt.Errorf("selected service account cannot be used while Connect environment is set; unset %s", strings.Join(conflicts, ", "))
		}
		meta, store, err := getServiceAccountMetadata(args[0])
		if err != nil {
			return err
		}
		token, err := serviceAccountSecrets.Get(cmd.Context(), meta.Name)
		if err != nil {
			return err
		}
		auth := opAuthContext{mode: "service-account-profile", serviceAccount: meta.Name, accountHint: meta.Account, tokenSource: "keychain", token: token, explicitToken: true}
		ctx := context.WithValue(cmd.Context(), opAuthContextKey{}, auth)
		_, _, runErr := newOpRunner().command(ctx, "vault", "list", "--format", "json")
		result := map[string]any{"auth_mode": auth.mode, "op_service_account": meta.Name, "account_hint": meta.Account, "token_source": "keychain", "authenticated": runErr == nil, "connect_env_conflicts": connectEnvConflicts()}
		if runErr != nil {
			return fmt.Errorf("service account %q failed verification: %w", meta.Name, runErr)
		}
		now := time.Now().UTC()
		meta.LastVerified = &now
		meta.UpdatedAt = now
		store.ServiceAccounts[meta.Name] = meta
		if err := saveServiceAccountStore(store); err != nil {
			return err
		}
		return flags.printJSON(cmd, result)
	}}
}

func newServiceAccountRepairAccessCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "repair-access <name>",
		Short: "Trust this signed CLI to read an existing token without future prompts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, _, err := getServiceAccountMetadata(name); err != nil {
				return err
			}
			if flags.dryRun {
				return flags.printJSON(cmd, map[string]any{"dry_run": true, "name": name, "would_repair_access": true})
			}
			if runtime.GOOS != "darwin" {
				return errors.New("Keychain access repair is available only on macOS")
			}
			if flags.noInput {
				return errors.New("Keychain access repair requires an interactive macOS session")
			}
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("Keychain access repair requires a terminal so macOS can request approval")
			}
			if err := repairKeychainAccess(cmd.Context(), serviceAccountKeychainService, name); err != nil {
				return fmt.Errorf("repairing service-account token %q: %w", name, err)
			}
			return flags.printJSON(cmd, map[string]any{
				"name":         name,
				"token_source": "keychain",
				"access":       "repaired",
			})
		},
	}
}

func newServiceAccountRemoveCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{Use: "remove <name>", Short: "Remove a named token and its metadata", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		_, store, err := getServiceAccountMetadata(name)
		if err != nil {
			return err
		}
		if flags.dryRun {
			return flags.printJSON(cmd, map[string]any{"dry_run": true, "name": name, "would_remove": true})
		}
		if !flags.yes {
			return errors.New("confirmation required: pass --yes")
		}
		token, err := serviceAccountSecrets.Get(cmd.Context(), name)
		if err != nil {
			return err
		}
		if err := serviceAccountSecrets.Delete(cmd.Context(), name); err != nil {
			return err
		}
		delete(store.ServiceAccounts, name)
		if err := saveServiceAccountStore(store); err != nil {
			if rollbackErr := serviceAccountSecrets.Set(context.WithoutCancel(cmd.Context()), name, token); rollbackErr != nil {
				return fmt.Errorf("%w; restoring removed Keychain token also failed: %v", err, rollbackErr)
			}
			return err
		}
		return flags.printJSON(cmd, map[string]any{"removed": name})
	}}
}
