//go:build !(cgo && kernforge_treesitter)

package main

func optionalTreeSitterStructuralIndexAdapter() structuralIndexAdapter {
	return nil
}

func optionalTreeSitterStructuralIndexNote() string {
	return "Tree-sitter adapter inactive in this build; build with -tags kernforge_treesitter and cgo to enable it."
}
