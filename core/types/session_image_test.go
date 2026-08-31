package types

import "testing"

func TestMessageImageContentsPreservesLegacyAndMultipleImages(t *testing.T) {
	message := Message{
		Image: &ImageContent{URL: "legacy"},
		Images: []ImageContent{
			{URL: "first"},
			{URL: "second"},
		},
	}

	images := message.ImageContents()
	if len(images) != 3 || images[0].URL != "legacy" || images[1].URL != "first" || images[2].URL != "second" {
		t.Fatalf("ImageContents() = %#v, want legacy image followed by multiple images", images)
	}
}
