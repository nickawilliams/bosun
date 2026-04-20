//go:build darwin || windows

package audio

import (
	"bytes"
	"time"

	mp3 "github.com/hajimehoshi/go-mp3"

	"github.com/ebitengine/oto/v3"
)

func play(data []byte) error {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return err
	}

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   decoder.SampleRate(),
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		return err
	}
	<-ready

	player := ctx.NewPlayer(decoder)
	player.Play()

	for player.IsPlaying() {
		time.Sleep(50 * time.Millisecond)
	}

	return nil
}
