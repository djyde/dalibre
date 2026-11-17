// Package plugindocs holds the plugin authors' guide, README.md, for
// dalibre-plugin docs to print.
package plugindocs

import _ "embed"

// Guide is README.md, Writing Dalibre plugins.
//
//go:embed README.md
var Guide string
