package cmd

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
)

// maxImages is the most reference images any model accepts.
const maxImages = 14

// Image types the API accepts as input, see interactions.ImageContentMimeType.
var imageTypes = []interactions.ImageContentMimeType{
	interactions.ImageContentMimeTypeImagePng,
	interactions.ImageContentMimeTypeImageJpeg,
	interactions.ImageContentMimeTypeImageWebp,
	interactions.ImageContentMimeTypeImageHeic,
	interactions.ImageContentMimeTypeImageHeif,
	interactions.ImageContentMimeTypeImageGif,
	interactions.ImageContentMimeTypeImageBmp,
	interactions.ImageContentMimeTypeImageTiff,
}

// Extensions for the types http.DetectContentType doesn't recognize.
var imageExtensions = map[string]interactions.ImageContentMimeType{
	".heic": interactions.ImageContentMimeTypeImageHeic,
	".heif": interactions.ImageContentMimeTypeImageHeif,
	".tif":  interactions.ImageContentMimeTypeImageTiff,
	".tiff": interactions.ImageContentMimeTypeImageTiff,
}

// imageType returns the MIME type of an image file, sniffed from its content
// (so a JPEG named .png is still sent as JPEG) or else from its extension.
func imageType(path string, data []byte) (interactions.ImageContentMimeType, error) {
	if t := interactions.ImageContentMimeType(http.DetectContentType(data)); slices.Contains(imageTypes, t) {
		return t, nil
	}
	if t, ok := imageExtensions[strings.ToLower(filepath.Ext(path))]; ok {
		return t, nil
	}
	return "", fmt.Errorf("%s: unsupported image type, use PNG, JPEG, WebP, HEIC, HEIF, GIF, BMP or TIFF", path)
}

// promptInput builds the request input: the prompt alone, or the prompt
// followed by the reference images, inlined as base64. camera, if not nil,
// takes a photo that is added last; it is only called once the files have
// been read, so a bad -i doesn't turn the camera on for nothing.
func promptInput(prompt string, images []string, camera func() ([]byte, error)) (*interactions.InteractionsInput, error) {
	n := len(images)
	if camera != nil {
		n++
	}
	if n == 0 {
		return genai.Ptr(interactions.NewInteractionsInput(prompt)), nil
	}
	if n > maxImages {
		return nil, usageError{fmt.Errorf("too many reference images: %d, at most %d", n, maxImages)}
	}
	contents := []interactions.Content{interactions.NewContent(interactions.TextContent{Text: prompt})}
	for _, path := range images {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading reference image: %w", err)
		}
		t, err := imageType(path, data)
		if err != nil {
			return nil, usageError{err}
		}
		contents = append(contents, interactions.NewContent(interactions.ImageContent{
			Data:     genai.Ptr(base64.StdEncoding.EncodeToString(data)),
			MimeType: t.ToPointer(),
		}))
	}
	if camera != nil {
		data, err := camera()
		if err != nil {
			return nil, err
		}
		contents = append(contents, interactions.NewContent(interactions.ImageContent{
			Data:     genai.Ptr(base64.StdEncoding.EncodeToString(data)),
			MimeType: interactions.ImageContentMimeTypeImageJpeg.ToPointer(),
		}))
	}
	return genai.Ptr(interactions.NewInteractionsInput(contents)), nil
}
