//go:build linux

package audio

import (
	"bytes"

	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

func play(data []byte) error {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return err
	}

	client, err := pulse.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()

	stream, err := client.NewPlayback(
		pulse.NewReader(decoder, proto.FormatInt16LE),
		pulse.PlaybackSampleRate(decoder.SampleRate()),
		pulse.PlaybackStereo,
	)
	if err != nil {
		return err
	}
	defer stream.Close()

	stream.Start()
	stream.Drain()

	return nil
}
