//go:build windows

package credentials

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// x/sys/windows does not expose constants for these allow ACE variants.
	accessAllowedObjectACE         = 5
	accessAllowedCallbackACE       = 9
	accessAllowedCallbackObjectACE = 11
)

func validateFileSecurityBeforeOpen(os.FileInfo) error { return nil }

func validateFileSecurity(file *os.File, _ os.FileInfo) error {
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("inspect Windows security descriptor: %w", err)
	}
	if descriptor == nil {
		return errors.New("credential file has no Windows security descriptor")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("inspect current Windows user: %w", err)
	}
	return validateWindowsSecurityDescriptor(descriptor, user.User.Sid)
}

func validateWindowsSecurityDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, user *windows.SID) error {
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("inspect Windows owner: %w", err)
	}
	if owner == nil || !owner.Equals(user) {
		return errors.New("must be owned by the current user")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("must have a Windows access control list")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve Windows SYSTEM identity: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Windows Administrators identity: %w", err)
	}
	ownerReadable := false
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("inspect Windows access control entry: %w", err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			sensitiveAccess := windows.ACCESS_MASK(
				windows.GENERIC_ALL | windows.GENERIC_READ |
					windows.GENERIC_WRITE | windows.GENERIC_EXECUTE |
					windows.FILE_READ_DATA | windows.FILE_WRITE_DATA |
					windows.FILE_APPEND_DATA | windows.FILE_EXECUTE |
					windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER,
			)
			accessesFile := ace.Mask&sensitiveAccess != 0
			if !accessesFile {
				continue
			}
			if sid.Equals(owner) {
				ownerReadable = ownerReadable ||
					ace.Mask&(windows.GENERIC_READ|windows.GENERIC_ALL|windows.FILE_READ_DATA) != 0
				continue
			}
			if !sid.Equals(system) && !sid.Equals(administrators) {
				return errors.New("must not be accessible by other Windows users")
			}
		case accessAllowedObjectACE, accessAllowedCallbackACE, accessAllowedCallbackObjectACE:
			return errors.New("must not grant access through advanced Windows access control entries")
		}
	}
	if !ownerReadable {
		return errors.New("must be readable by its owner")
	}
	return nil
}
