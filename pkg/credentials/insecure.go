// Package credentials classifies guest credentials without exposing them in diagnostics.
package credentials

import "unicode/utf8"

// IsKnownInsecure recognizes documented defaults and passwords shorter than 12 characters.
func IsKnownInsecure(user, password string) bool {
	if utf8.RuneCountInString(password) < 12 {
		return true
	}
	for _, pair := range [][2]string{{"", "P@ssw0rd!"}, {"admin", "admin"}, {"packer", "packer"}, {"vagrant", "vagrant"}} {
		if password == pair[1] && (pair[0] == "" || user == pair[0]) {
			return true
		}
	}
	return false
}
