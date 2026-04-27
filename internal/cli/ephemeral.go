package cli

import (
	"fmt"
	"math/rand/v2"
)

var (
	ephemeralAdjectives = []string{
		"brave", "swift", "mighty", "clever", "bright", "noble",
		"wise", "bold", "calm", "keen", "quiet", "lopsided",
		"wobbly", "fleeting", "glittering", "flying",
	}
	ephemeralNouns = []string{
		"falcon", "eagle", "wolf", "bear", "lion", "tiger",
		"hawk", "owl", "fox", "deer", "snake", "turtle",
		"rabbit", "fish", "bird", "catdog", "horse", "monkey",
		"gorilla", "dragon", "unicorn",
	}
)

// generateEphemeralName returns a random adjective-noun pair suitable for
// use as an ephemeral environment name (e.g., "brave-falcon").
func generateEphemeralName() string {
	adj := ephemeralAdjectives[rand.IntN(len(ephemeralAdjectives))]
	noun := ephemeralNouns[rand.IntN(len(ephemeralNouns))]
	return fmt.Sprintf("%s-%s", adj, noun)
}
