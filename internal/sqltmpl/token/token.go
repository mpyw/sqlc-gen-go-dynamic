// Package token defines the token kinds of the template layer.
//
// SQL is not parsed as a grammar and no keyword is recognized. The lexer distinguishes
// directive comments, bind markers, and the quoted and commented spans that could otherwise
// hide one; everything else is opaque Text.
package token

// Kind is a token kind.
type Kind uint8

const (
	EOF Kind = iota

	// Text is opaque template text, emitted verbatim. Plain comments and quoted spans are
	// part of it.
	Text

	// Bind is a parameter marker: @name, sqlc.arg(name), sqlc.narg(name), sqlc.slice(name).
	Bind

	// Directives.
	If
	Elseif
	Else
	For
	End
)
