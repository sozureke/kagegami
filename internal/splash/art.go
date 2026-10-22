package splash

import _ "embed" // the art is part of the binary

// Art is the art of the splash, from assets/art.txt.
//
//go:embed assets/art.txt
var Art string
