//go:build darwin && cgo

// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static OSStatus pp_create_keychain_access(
    const char *trusted_path, SecAccessRef *access) {
    SecTrustedApplicationRef trusted = NULL;
    OSStatus status = SecTrustedApplicationCreateFromPath(trusted_path, &trusted);
    if (status != errSecSuccess) return status;

    const void *values[] = { trusted };
    CFArrayRef trusted_list = CFArrayCreate(
        kCFAllocatorDefault, values, 1, &kCFTypeArrayCallBacks);
    CFRelease(trusted);
    if (trusted_list == NULL) return errSecAllocate;

    status = SecAccessCreate(
        CFSTR("1password-pp-cli named service-account token"),
        trusted_list, access);
    CFRelease(trusted_list);
    return status;
}

static OSStatus pp_add_keychain_secret(
    const char *service, UInt32 service_len,
    const char *account, UInt32 account_len,
    const void *secret, UInt32 secret_len,
    SecAccessRef access) {
    CFStringRef service_value = CFStringCreateWithBytes(
        kCFAllocatorDefault, (const UInt8 *)service, service_len,
        kCFStringEncodingUTF8, false);
    CFStringRef account_value = CFStringCreateWithBytes(
        kCFAllocatorDefault, (const UInt8 *)account, account_len,
        kCFStringEncodingUTF8, false);
    CFDataRef secret_value = CFDataCreate(
        kCFAllocatorDefault, (const UInt8 *)secret, secret_len);
    if (service_value == NULL || account_value == NULL || secret_value == NULL) {
        if (service_value != NULL) CFRelease(service_value);
        if (account_value != NULL) CFRelease(account_value);
        if (secret_value != NULL) CFRelease(secret_value);
        return errSecAllocate;
    }

    const void *keys[] = {
        kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData, kSecAttrAccess
    };
    const void *values[] = {
        kSecClassGenericPassword, service_value, account_value, secret_value, access
    };
    CFDictionaryRef query = CFDictionaryCreate(
        kCFAllocatorDefault, keys, values, 5,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFRelease(service_value);
    CFRelease(account_value);
    CFRelease(secret_value);
    if (query == NULL) return errSecAllocate;
    OSStatus status = SecItemAdd(query, NULL);
    CFRelease(query);
    return status;
}

static OSStatus pp_set_keychain_secret(
    const char *service, UInt32 service_len,
    const char *account, UInt32 account_len,
    const void *secret, UInt32 secret_len,
    const char *trusted_path) {
    SecAccessRef access = NULL;
    OSStatus status = pp_create_keychain_access(trusted_path, &access);
    if (status != errSecSuccess) return status;

    SecKeychainItemRef item = NULL;
    status = SecKeychainFindGenericPassword(
        NULL, service_len, service, account_len, account,
        NULL, NULL, &item);
    if (status == errSecSuccess) {
        status = SecKeychainItemSetAccess(item, access);
        if (status == errSecSuccess) {
            status = SecKeychainItemModifyAttributesAndData(item, NULL, secret_len, secret);
        }
        CFRelease(item);
        CFRelease(access);
        return status;
    }
    if (status != errSecItemNotFound) {
        CFRelease(access);
        return status;
    }
    status = pp_add_keychain_secret(
        service, service_len, account, account_len,
        secret, secret_len, access);
    CFRelease(access);
    return status;
}

static void pp_zero(void *p, size_t n) {
    if (p != NULL && n > 0) {
        memset_s(p, n, 0, n);
    }
}

static OSStatus pp_get_keychain_secret(
    const char *service, UInt32 service_len,
    const char *account, UInt32 account_len,
    void **secret, UInt32 *secret_len) {
    return SecKeychainFindGenericPassword(NULL, service_len, service,
        account_len, account, secret_len, secret, NULL);
}

static void pp_free_keychain_secret(void *secret, UInt32 secret_len) {
    if (secret != NULL) {
        pp_zero(secret, secret_len);
        SecKeychainItemFreeContent(NULL, secret);
    }
}

static OSStatus pp_delete_keychain_secret(
    const char *service, UInt32 service_len,
    const char *account, UInt32 account_len) {
    SecKeychainItemRef item = NULL;
    OSStatus status = SecKeychainFindGenericPassword(NULL, service_len, service,
        account_len, account, NULL, NULL, &item);
    if (status != errSecSuccess) return status;
    status = SecKeychainItemDelete(item);
    CFRelease(item);
    return status;
}

static OSStatus pp_repair_keychain_access(
    const char *service, UInt32 service_len,
    const char *account, UInt32 account_len,
    const char *trusted_path) {
    SecKeychainItemRef item = NULL;
    OSStatus status = SecKeychainFindGenericPassword(NULL, service_len, service,
        account_len, account, NULL, NULL, &item);
    if (status != errSecSuccess) return status;

    SecAccessRef access = NULL;
    status = pp_create_keychain_access(trusted_path, &access);
    if (status == errSecSuccess) {
        status = SecKeychainItemSetAccess(item, access);
        CFRelease(access);
    }
    CFRelease(item);
    return status;
}
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

func trustedExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving executable for Keychain access: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving executable symlinks for Keychain access: %w", err)
	}
	return resolved, nil
}

func setKeychainSecret(ctx context.Context, service, account, secret string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trustedPath, err := trustedExecutablePath()
	if err != nil {
		return err
	}
	cService := C.CString(service)
	cAccount := C.CString(account)
	cTrustedPath := C.CString(trustedPath)
	cSecret := C.CBytes([]byte(secret))
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cAccount))
	defer C.free(unsafe.Pointer(cTrustedPath))
	defer func() {
		C.pp_zero(cSecret, C.size_t(len(secret)))
		C.free(cSecret)
	}()
	status := C.pp_set_keychain_secret(
		cService, C.UInt32(len(service)),
		cAccount, C.UInt32(len(account)),
		cSecret, C.UInt32(len(secret)),
		cTrustedPath,
	)
	if status != C.errSecSuccess {
		return fmt.Errorf("saving service-account token in Keychain failed (OSStatus %d)", int32(status))
	}
	return nil
}

func repairKeychainAccess(ctx context.Context, service, account string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trustedPath, err := trustedExecutablePath()
	if err != nil {
		return err
	}
	cService, cAccount, cTrustedPath := C.CString(service), C.CString(account), C.CString(trustedPath)
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cAccount))
	defer C.free(unsafe.Pointer(cTrustedPath))
	status := C.pp_repair_keychain_access(
		cService, C.UInt32(len(service)), cAccount, C.UInt32(len(account)), cTrustedPath)
	if status != C.errSecSuccess {
		return fmt.Errorf("repairing Keychain access failed (OSStatus %d)", int32(status))
	}
	return nil
}

func getKeychainSecret(ctx context.Context, service, account string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cService, cAccount := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cAccount))
	var secret unsafe.Pointer
	var secretLen C.UInt32
	status := C.pp_get_keychain_secret(cService, C.UInt32(len(service)), cAccount, C.UInt32(len(account)), &secret, &secretLen)
	if status == C.errSecItemNotFound {
		return "", errServiceAccountSecretNotFound
	}
	if status != C.errSecSuccess {
		return "", fmt.Errorf("Keychain lookup failed (OSStatus %d)", int32(status))
	}
	defer C.pp_free_keychain_secret(secret, secretLen)
	return string(C.GoBytes(secret, C.int(secretLen))), nil
}

func deleteKeychainSecret(ctx context.Context, service, account string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cService, cAccount := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cAccount))
	status := C.pp_delete_keychain_secret(cService, C.UInt32(len(service)), cAccount, C.UInt32(len(account)))
	if status != C.errSecSuccess {
		return fmt.Errorf("Keychain delete failed (OSStatus %d)", int32(status))
	}
	return nil
}
