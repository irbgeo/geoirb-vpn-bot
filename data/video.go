// Package data holds the files shipped inside the bot binary.
package data

import _ "embed"

//go:embed SplitTunnel.mp4
var splitTunnel []byte

// SplitTunnel is the video on app split tunneling in AmneziaVPN (recorded
// on Android, the same steps on Windows). To change it, replace the file
// and deploy.
func SplitTunnel() []byte {
	return splitTunnel
}
