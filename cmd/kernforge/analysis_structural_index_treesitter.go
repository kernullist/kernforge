//go:build cgo && kernforge_treesitter

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	treesitterc "github.com/smacker/go-tree-sitter/c"
	treesittercpp "github.com/smacker/go-tree-sitter/cpp"
	treesittergo "github.com/smacker/go-tree-sitter/golang"
)

type treeSitterStructuralIndexAdapter struct{}

func optionalTreeSitterStructuralIndexAdapter() structuralIndexAdapter {
	return treeSitterStructuralIndexAdapter{}
}

func optionalTreeSitterStructuralIndexNote() string {
	return "Tree-sitter adapter active: using github.com/smacker/go-tree-sitter behind cgo and kernforge_treesitter build tag."
}

func (treeSitterStructuralIndexAdapter) Name() string {
	return structuralParserTreeSitter
}

func (treeSitterStructuralIndexAdapter) Supports(file ScannedFile) bool {
	switch strings.ToLower(strings.TrimSpace(file.Extension)) {
	case ".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".inl":
		return true
	default:
		return false
	}
}

func (treeSitterStructuralIndexAdapter) Extract(snapshot ProjectSnapshot, file ScannedFile, text string) (structuralFileExtraction, error) {
	language := treeSitterLanguageForFile(file)
	if language == nil {
		return structuralFileExtraction{}, fmt.Errorf("no tree-sitter language for %s", file.Extension)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(language)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tree, err := parser.ParseCtx(ctx, nil, []byte(text))
	if err != nil {
		return structuralFileExtraction{}, err
	}
	defer tree.Close()

	out := structuralFileExtraction{
		Parser: structuralParserTreeSitter,
		Status: "indexed",
	}
	root := tree.RootNode()
	treeSitterCollectSymbols(snapshot, file, text, root, "", "", &out)
	out.References = append(out.References, structuralFileImportReferences(file)...)
	if root.HasError() {
		out.Diagnostics = append(out.Diagnostics, StructuralIndexDiagnostic{
			Path:     file.Path,
			Parser:   structuralParserTreeSitter,
			Severity: "warning",
			Reason:   "parse_error_nodes",
			Detail:   "tree-sitter parsed the file but reported error nodes",
		})
	}
	if len(out.Symbols) == 0 && len(out.References) == 0 {
		out.Status = "tree_sitter_empty"
		out.Diagnostic = "tree-sitter parsed the file but found no structural symbols"
	}
	return out, nil
}

func treeSitterLanguageForFile(file ScannedFile) *sitter.Language {
	switch strings.ToLower(strings.TrimSpace(file.Extension)) {
	case ".go":
		return treesittergo.GetLanguage()
	case ".c":
		return treesitterc.GetLanguage()
	case ".h":
		return treesittercpp.GetLanguage()
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".inl":
		return treesittercpp.GetLanguage()
	default:
		return nil
	}
}

func treeSitterCollectSymbols(snapshot ProjectSnapshot, file ScannedFile, text string, node *sitter.Node, parentID string, parentName string, out *structuralFileExtraction) {
	if node == nil {
		return
	}
	nodeType := node.Type()
	name, kind := treeSitterSymbolNameAndKind(node, text)
	currentParent := parentID
	currentParentName := parentName
	if name != "" && kind != "" {
		startPoint := node.StartPoint()
		endPoint := node.EndPoint()
		startLine := int(startPoint.Row) + 1
		endLine := int(endPoint.Row) + 1
		canonicalName := name
		if parentName != "" {
			canonicalName = parentName + "::" + name
		}
		symbolID := buildSourceAnchorID(kind, canonicalName, file.Path)
		currentParent = symbolID
		currentParentName = canonicalName
		out.Symbols = append(out.Symbols, SymbolRecord{
			ID:                symbolID,
			Name:              analysisShortCStyleName(name),
			CanonicalName:     canonicalName,
			Kind:              kind,
			Language:          analysisLanguageForExtension(file.Extension),
			File:              file.Path,
			Module:            unrealModuleForFile(snapshot, file.Path),
			ContainerSymbolID: parentID,
			BuildContextID:    firstSliceValue(buildContextIDsForFile(snapshot, file.Path)),
			Signature:         compactPromptSection(strings.TrimSpace(node.Content([]byte(text))), 240),
			StartLine:         startLine,
			EndLine:           endLine,
			StartByte:         int(node.StartByte()),
			EndByte:           int(node.EndByte()),
			ExtractionMethod:  structuralParserTreeSitter,
			Tags:              analysisUniqueStrings([]string{"tree_sitter", nodeType}),
		})
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		treeSitterCollectSymbols(snapshot, file, text, node.NamedChild(i), currentParent, currentParentName, out)
	}
}

func treeSitterSymbolNameAndKind(node *sitter.Node, text string) (string, string) {
	if node == nil {
		return "", ""
	}
	nodeType := node.Type()
	switch nodeType {
	case "function_declaration", "method_declaration", "function_definition":
		return treeSitterNameFromNode(node, text), "function"
	case "type_spec", "type_declaration":
		return treeSitterNameFromNode(node, text), "type"
	case "class_specifier":
		return treeSitterNameFromNode(node, text), "class"
	case "struct_specifier":
		return treeSitterNameFromNode(node, text), "struct"
	case "namespace_definition":
		return treeSitterNameFromNode(node, text), "namespace"
	case "enum_specifier":
		return treeSitterNameFromNode(node, text), "enum"
	case "preproc_def":
		return treeSitterNameFromNode(node, text), "macro"
	case "preproc_function_def":
		return treeSitterNameFromNode(node, text), "function_macro"
	default:
		return "", ""
	}
}

func treeSitterNameFromNode(node *sitter.Node, text string) string {
	if node == nil {
		return ""
	}
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		return strings.TrimSpace(nameNode.Content([]byte(text)))
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier", "field_identifier", "type_identifier", "namespace_identifier", "qualified_identifier":
			return strings.TrimSpace(child.Content([]byte(text)))
		case "function_declarator", "pointer_declarator", "reference_declarator":
			if name := treeSitterNameFromNode(child, text); name != "" {
				return name
			}
		}
	}
	return ""
}
