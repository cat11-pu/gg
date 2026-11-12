package gg

import (
	"io/ioutil"

	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
)

// LoadTextFace loads a TrueType font at the given point size and returns a
// textFace ready for a TextRenderer. Full hinting is enabled and a 2x face is
// built alongside the normal one to support supersampled anti-aliasing at
// small sizes.
func LoadTextFace(path string, points float64) (*textFace, error) {
	fontBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return NewTextFace(fontBytes, points)
}

// NewTextFace parses raw TTF bytes into a textFace at the given point size.
func NewTextFace(ttf []byte, points float64) (*textFace, error) {
	parsed, err := truetype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	opts := &truetype.Options{
		Size:    points,
		Hinting: font.HintingFull,
	}
	face := truetype.NewFace(parsed, opts)
	face2x := truetype.NewFace(parsed, &truetype.Options{
		Size:    points * supersample,
		Hinting: font.HintingFull,
	})
	return &textFace{
		face:   face,
		face2x: face2x,
		font:   parsed,
		size:   points,
	}, nil
}

// LoadTextFontSet loads several TTF files at the same point size and combines
// them into one TextRenderer. Earlier faces take precedence: a rune is drawn
// by the first face containing it, which lets a Latin face with a kern table
// be combined with a CJK fallback face for mixed Chinese/English text.
func LoadTextFontSet(points float64, maxEntries, maxBytes int, paths ...string) (*TextRenderer, error) {
	faces := make([]*textFace, 0, len(paths))
	for _, p := range paths {
		tf, err := LoadTextFace(p, points)
		if err != nil {
			return nil, err
		}
		faces = append(faces, tf)
	}
	return NewTextRenderer(maxEntries, maxBytes, faces...), nil
}
