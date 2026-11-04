package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// dirSDDL: owned by Administrators; SYSTEM and Administrators full control, Users read
// (the window opens service.log). ProgramData subfolders are user-writable by default, and
// the service trusts what is in here: config.json args run in ciadpi as SYSTEM, state files
// are written back to the registry and adapters.
const dirSDDL = "O:BAG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"

// SecureDir creates Dir() if needed and resets its owner and permissions, and those of
// everything already in it, so a folder or file a user planted before install is not
// trusted. Needs administrator or SYSTEM.
func SecureDir() error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(dirSDDL)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil); err != nil {
		return err
	}
	// Children: drop any explicit entries and inherit only from the folder. (An empty ACL
	// from SDDL: ACLFromEntries with no entries gets a NULL ACL back and panics.)
	esd, err := windows.SecurityDescriptorFromString("D:")
	if err != nil {
		return err
	}
	empty, _, err := esd.DACL()
	if err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		return windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
			owner, nil, empty, nil)
	})
}
