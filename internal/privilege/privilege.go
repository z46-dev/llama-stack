package privilege

import (
	"fmt"
	"os"
	"os/user"
	"slices"
)

// RequireAdmin permits privileged work only for root acting directly or through an approved sudo user.
func RequireAdmin(groupName string) (err error) {
	var sudoUser string = os.Getenv("SUDO_USER")

	if os.Geteuid() != 0 {
		err = fmt.Errorf("this command changes system state; rerun it with sudo as a member of %s", groupName)
		return
	}

	if sudoUser == "" || sudoUser == "root" {
		return
	}

	err = requireGroupMembership(sudoUser, groupName)
	return
}

// requireGroupMembership verifies that username belongs to groupName.
func requireGroupMembership(username, groupName string) (err error) {
	var (
		account *user.User
		group   *user.Group
		groups  []string
	)

	if account, err = user.Lookup(username); err != nil {
		err = fmt.Errorf("look up sudo user %q: %w", username, err)
		return
	}

	if group, err = user.LookupGroup(groupName); err != nil {
		err = fmt.Errorf("required administrative group %q does not exist: %w", groupName, err)
		return
	}

	if groups, err = account.GroupIds(); err != nil {
		err = fmt.Errorf("list groups for %q: %w", username, err)
		return
	}

	if !slices.Contains(groups, group.Gid) {
		err = fmt.Errorf("sudo user %q is not a member of %s", username, groupName)
	}

	return
}
