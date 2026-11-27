package public

import "embed"

//go:embed all:dist
var Public embed.FS

// WoPanFormJS is the storage-form helper snippet (the "send SMS code" button).
// It lives outside dist on purpose: dist is upstream's compiled bundle and CI
// re-downloads it on every build, so anything placed there is lost.
//
//go:embed wopan_form.js
var WoPanFormJS []byte
