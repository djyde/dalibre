package mcp

import "slices"

// readTools are the tools every agent gets.
var readTools = []string{"search_books", "get_book", "list_facets"}

// Group is a kind of tool, as Settings lists them.
type Group int

const (
	// ReadGroup searches and reads the library; agents always have it.
	ReadGroup Group = iota
	// ChangeGroup changes the library, while changes are allowed.
	ChangeGroup
	// SourceGroup is Get books' sources and the metadata plugins, while
	// changes are allowed.
	SourceGroup
	// PluginGroup writes, adds and tries plugins, while changes are
	// allowed.
	PluginGroup
)

// Tools are the names of a group's tools, in the order Settings lists
// them.
func Tools(g Group) []string {
	switch g {
	case ChangeGroup:
		return slices.Clone(writeTools)
	case SourceGroup:
		return slices.Clone(sourceTools)
	case PluginGroup:
		return slices.Clone(pluginTools)
	}
	return slices.Clone(readTools)
}
