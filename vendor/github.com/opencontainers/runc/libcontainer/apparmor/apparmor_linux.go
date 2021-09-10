package apparmor

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"sync"

	"github.com/opencontainers/runc/libcontainer/utils"
)

<<<<<<< HEAD
var (
	appArmorEnabled bool
	checkAppArmor   sync.Once
)

// isEnabled returns true if apparmor is enabled for the host.
func isEnabled() bool {
	checkAppArmor.Do(func() {
		if _, err := os.Stat("/sys/kernel/security/apparmor"); err == nil {
			buf, err := ioutil.ReadFile("/sys/module/apparmor/parameters/enabled")
			appArmorEnabled = err == nil && len(buf) > 1 && buf[0] == 'Y'
		}
	})
	return appArmorEnabled
||||||| 5e58841cce7
// IsEnabled returns true if apparmor is enabled for the host.
func IsEnabled() bool {
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err == nil {
		buf, err := ioutil.ReadFile("/sys/module/apparmor/parameters/enabled")
		return err == nil && bytes.HasPrefix(buf, []byte("Y"))
	}
	return false
=======
var (
	appArmorEnabled bool
	checkAppArmor   sync.Once
)

// IsEnabled returns true if apparmor is enabled for the host.
func IsEnabled() bool {
	checkAppArmor.Do(func() {
		if _, err := os.Stat("/sys/kernel/security/apparmor"); err == nil {
			buf, err := ioutil.ReadFile("/sys/module/apparmor/parameters/enabled")
			appArmorEnabled = err == nil && len(buf) > 1 && buf[0] == 'Y'
		}
	})
	return appArmorEnabled
>>>>>>> v1.21.4
}

func setProcAttr(attr, value string) error {
	// Under AppArmor you can only change your own attr, so use /proc/self/
	// instead of /proc/<tid>/ like libapparmor does
	attrPath := "/proc/self/attr/apparmor/" + attr
	if _, err := os.Stat(attrPath); errors.Is(err, os.ErrNotExist) {
		// fall back to the old convention
		attrPath = "/proc/self/attr/" + attr
	}

	f, err := os.OpenFile(attrPath, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := utils.EnsureProcHandle(f); err != nil {
		return err
	}

	_, err = f.WriteString(value)
	return err
}

// changeOnExec reimplements aa_change_onexec from libapparmor in Go
func changeOnExec(name string) error {
	if err := setProcAttr("exec", "exec "+name); err != nil {
		return fmt.Errorf("apparmor failed to apply profile: %s", err)
	}
	return nil
}

// applyProfile will apply the profile with the specified name to the process after
// the next exec. It is only supported on Linux and produces an error on other
// platforms.
func applyProfile(name string) error {
	if name == "" {
		return nil
	}

	return changeOnExec(name)
}
