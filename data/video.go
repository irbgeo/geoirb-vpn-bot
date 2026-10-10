// Package data holds the files shipped inside the bot binary.
package data

import "embed"

// videos: every mp4 next to this file, so a video that is not there yet
// (IPhoneSplitTunnel.mp4) is not a build error.
//
//go:embed *.mp4
var videos embed.FS

// AndroidSplitTunnel is the video on app split tunneling in AmneziaVPN (recorded
// on Android, the same steps on Windows). To change it, replace the file
// and deploy.
func AndroidSplitTunnel() []byte {
	return video("AndroidSplitTunnel.mp4")
}

// IPhoneSplitTunnel is the video on the Shortcuts automation that switches
// the tunnel off while a chosen app is open on iPhone and iPad; nil while
// data/IPhoneSplitTunnel.mp4 is not there. To ship it, add the file and
// deploy.
func IPhoneSplitTunnel() []byte {
	return video("IPhoneSplitTunnel.mp4")
}

// video is the embedded file name; nil when there is no such file.
func video(name string) []byte {
	b, err := videos.ReadFile(name)
	if err != nil {
		return nil
	}
	return b
}
