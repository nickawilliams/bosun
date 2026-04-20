package audio

import _ "embed"

//go:embed bosun-pipe.mp3
var whistle []byte

// Play decodes and plays the embedded bosun whistle sound.
// Errors are silently ignored — this is an easter egg.
func Play() {
	_ = play(whistle)
}
