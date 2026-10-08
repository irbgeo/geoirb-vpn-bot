// Package bypass holds the split-tunneling lists (Russian sites and
// networks that must NOT go through the VPN) in the Amnezia app's import
// format. They are built into the binary: to update them, replace the
// files in lists/ and deploy.
package bypass

import (
	"context"
	"embed"
)

// Names of the lists as users get them.
const (
	ComputerList = "ru-sites-computer.json" // domains + IPs: the Amnezia desktop app
	PhoneList    = "ru-sites-phone.json"    // networks only: phones (desktop too)
)

//go:embed lists/*.json
var lists embed.FS

// builtinLists serves the built-in lists.
type builtinLists struct{}

// New creates the built-in lists.
func New() builtinLists {
	return builtinLists{}
}

// Files returns the lists, the computer one first. ReadFile copies, so each
// call gets its own bytes.
func (s builtinLists) Files(context.Context) ([]File, error) {
	out := make([]File, 0, 2)
	for _, name := range []string{
		ComputerList,
		PhoneList,
	} {
		data, err := lists.ReadFile("lists/" + name)
		if err != nil {
			return nil, err
		}
		file := File{
			Name: name,
			Data: data,
		}
		out = append(out, file)
	}
	return out, nil
}
