package project

import (
	"os"
	"path/filepath"
	"strings"
)

// GitignoreHeader is the first line of the default .gitignore Marshal writes
// when it starts a repository for a folder that had none.
const GitignoreHeader = "# Written by Marshal when it started this repository. Edit freely."

// OwnedFiles are the files Marshal itself adds at a project's root when it
// sets the project up. They are Marshal's, not the person's project.
var OwnedFiles = []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"}

// MarshalOwnedFile reports whether path, relative to the project root, is a
// file Marshal added: one of OwnedFiles, or a .gitignore that is still the one
// Marshal wrote (it starts with GitignoreHeader).
func MarshalOwnedFile(root, path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	for _, name := range OwnedFiles {
		if cleaned == name {
			return true
		}
	}
	if cleaned != ".gitignore" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	return err == nil && strings.HasPrefix(string(data), GitignoreHeader+"\n")
}
