//go:build windows

package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLoadFileEnforcesWindowsOwnerAndACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	setCredentialACL(t, path, user.User.Sid)
	if key, err := LoadFile(path); err != nil || key != "secret" {
		t.Fatalf("LoadFile = %q, %v", key, err)
	}

	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	setCredentialACL(t, path, user.User.Sid, everyone)
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "other Windows users") {
		t.Fatalf("permissive ACL error = %v", err)
	}
}

func TestWindowsSecurityDescriptorRejectsAnotherOwner(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:WDD:P(A;;GR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsSecurityDescriptor(descriptor, user.User.Sid); err == nil ||
		!strings.Contains(err.Error(), "owned by the current user") {
		t.Fatalf("different owner error = %v", err)
	}
}

func setCredentialACL(t *testing.T, path string, owner *windows.SID, others ...*windows.SID) {
	t.Helper()
	readers := append([]*windows.SID{owner}, others...)
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(readers))
	for _, reader := range readers {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(reader),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	runtime.KeepAlive(readers)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|
			windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
}
