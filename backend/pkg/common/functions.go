package common

import "strings"

func IsValidGitURL(u string) bool {
	validPrefixes := []string{
		"https://github.com/",
		"https://gitlab.com/",
		"https://bitbucket.org/",
		"git@github.com:",
		"git@gitlab.com:",
		"git@bitbucket.org:",
	}
	for _, p := range validPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}
