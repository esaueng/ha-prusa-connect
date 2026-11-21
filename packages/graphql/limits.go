package main

import (
	"errors"
	"fmt"

	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
)

// What one query may ask for before it runs.
//
// The text is already bounded at maxQueryBytes, and a short text can still ask
// for a great deal. An alias asks for one field again under another name, and a
// fragment spread inside an aliased field repeats the whole fragment, so a
// query a few hundred bytes long can expand to millions of fields. The bounds
// are counted on the expanded query, with every fragment spread written out,
// because that is what the executor walks.
//
// The standard introspection query a GraphQL client sends sets the depth and
// the field bound. It nests a type reference nine levels down under
// __schema, types, fields, args and type, which is fifteen fields deep, and it
// selects a little over two hundred fields with its fragments written out. It
// uses no alias, so the alias bound is free to be the small one.
const (
	maxQueryDepth   = 15
	maxQueryFields  = 512
	maxQueryAliases = 64
	maxQuerySteps   = 2048
)

// errQueryTooComplex reports a query over one of the bounds above.
var errQueryTooComplex = errors.New("the query asks for more than this module runs")

// errInvalidFragment reports a fragment spread that names no fragment in the
// document or forms a cycle. Refusing these before expansion keeps invalid
// fragment graphs from burning traversal budget before validation runs.
var errInvalidFragment = errors.New("the query names an unknown or cyclic fragment")

// errWriteOverGet reports a mutation or a subscription sent as a GET or HEAD.
var errWriteOverGet = errors.New("a mutation or a subscription is sent as a POST")

// parsed is a query the module read before running it. A document the parser
// refuses is nil here, and graphql.Do answers it with the syntax error.
func parsed(query string) *ast.Document {
	document, err := parser.Parse(parser.ParseParams{
		Source:  query,
		Options: parser.ParseOptions{NoLocation: true},
	})
	if err != nil {
		return nil
	}

	return document
}

// writesOverGet reports whether a GET would run a mutation or a subscription.
//
// The host takes view_resource before a GET and operate_resource before a
// POST, so a mutation in the URL runs under the read permission. A browser
// also follows a link or loads an image as a GET with the person's cookie, so
// a write over GET is one any page can make a visitor send.
//
// With an operation name the named operation decides. Without one, any write
// in the document refuses the request: graphql.Do runs the only operation of a
// document holding one and refuses a document holding more.
func writesOverGet(document *ast.Document, operation string) bool {
	for _, definition := range document.Definitions {
		op, ok := definition.(*ast.OperationDefinition)
		if !ok {
			continue
		}

		if operation != "" && (op.Name == nil || op.Name.Value != operation) {
			continue
		}

		if op.Operation == ast.OperationTypeMutation || op.Operation == ast.OperationTypeSubscription {
			return true
		}
	}

	return false
}

// bound refuses a document over maxQueryDepth, maxQueryFields or
// maxQueryAliases. Every operation is counted, named or not, because graphql.Do
// picks the one it runs after this check.
func bound(document *ast.Document) error {
	count := &counter{fragments: map[string]*ast.FragmentDefinition{}, entered: map[string]bool{}}

	for _, definition := range document.Definitions {
		if fragment, ok := definition.(*ast.FragmentDefinition); ok && fragment.Name != nil {
			count.fragments[fragment.Name.Value] = fragment
		}
	}

	for _, definition := range document.Definitions {
		op, ok := definition.(*ast.OperationDefinition)
		if !ok {
			continue
		}

		if err := count.selections(op.SelectionSet, 0); err != nil {
			return err
		}
	}

	return nil
}

// counter walks a document with its fragments written out. It stops at the
// first bound crossed, so the walk itself costs no more than the bounds allow.
type counter struct {
	fragments map[string]*ast.FragmentDefinition
	// entered holds the fragments on the path being walked so a cycle is
	// refused before it expands.
	entered map[string]bool
	steps   int
	fields  int
	aliases int
}

func (c *counter) selections(set *ast.SelectionSet, depth int) error {
	if set == nil {
		return nil
	}

	for _, selection := range set.Selections {
		if err := c.selection(selection, depth); err != nil {
			return err
		}
	}

	return nil
}

func (c *counter) selection(selection ast.Selection, depth int) error {
	c.steps++
	if c.steps > maxQuerySteps {
		return tooComplex("expands past %d selections", maxQuerySteps)
	}

	switch node := selection.(type) {
	case *ast.Field:
		return c.field(node, depth+1)
	case *ast.InlineFragment:
		return c.selections(node.SelectionSet, depth)
	case *ast.FragmentSpread:
		if node.Name == nil {
			return fmt.Errorf("%w: fragment spread has no name", errInvalidFragment)
		}

		name := node.Name.Value

		fragment, known := c.fragments[name]
		if !known {
			return fmt.Errorf("%w: fragment %q is not defined", errInvalidFragment, name)
		}

		if c.entered[name] {
			return fmt.Errorf("%w: fragment %q spreads itself", errInvalidFragment, name)
		}

		c.entered[name] = true
		defer delete(c.entered, name)

		return c.selections(fragment.SelectionSet, depth)
	}

	return nil
}

func (c *counter) field(field *ast.Field, depth int) error {
	c.fields++
	if field.Alias != nil {
		c.aliases++
	}

	switch {
	case depth > maxQueryDepth:
		return tooComplex("nests deeper than %d fields", maxQueryDepth)
	case c.fields > maxQueryFields:
		return tooComplex("selects more than %d fields", maxQueryFields)
	case c.aliases > maxQueryAliases:
		return tooComplex("carries more than %d aliases", maxQueryAliases)
	}

	return c.selections(field.SelectionSet, depth)
}

func tooComplex(format string, bound int) error {
	return fmt.Errorf("%w: it %s, counting every fragment where it is spread",
		errQueryTooComplex, fmt.Sprintf(format, bound))
}
