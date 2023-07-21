package kdl

import (
	"errors"
	"fmt"
	"io"
)

var errUnexpectedSemicolon = fmt.Errorf("%w: unexpected ';' not terminating a node", ErrInvalidSyntax)
var errUnexpectedRightBracket = fmt.Errorf("%w: unexpected top-level '}'", ErrInvalidSyntax)

func readNodes(r *reader) (nodes []Node, err error) {

	nodes = make([]Node, 0, 4)

	for {
		for {
			err = readUntilSignificant(r)
			if err != nil {
				if errors.Is(err, io.EOF) && r.depth == 0 {
					err = nil
				}
				return
			}

			var ch rune
			ch, err = r.peekRune()
			if err != nil {
				return
			}

			if !isNewLine(ch) {
				if ch == ';' {
					err = errUnexpectedSemicolon
					return
				} else if ch == '}' {
					if r.depth == 0 {
						err = errUnexpectedRightBracket
					}
					return
				}
				break
			}

			err = skipUntilNewLine(r, true)
			if err != nil {
				return
			}
		}

		// A "slashdash" comment silences the whole node
		var slashdash bool
		slashdash, err = r.isNext(charsSlashDash[:])
		if err != nil {
			return
		}
		if slashdash {
			r.discardBytes(2)
		}

		var node Node
		node, err = readNode(r)
		if err != nil {
			return
		}

		if !slashdash {
			nodes = append(nodes, node)
		}
	}
}

func readNode(r *reader) (Node, error) {

	node := NewNode("")

	hint, err := readMaybeTypeHint(r)
	if err != nil {
		return node, err
	}
	node.TypeHint = hint

	name, err, _ := readIdentifier(r, stopModeFreestanding)
	if err != nil {
		return node, err
	}

	node.Name = name

	for {

		err = readUntilSignificant(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return node, nil
			}
			return node, err
		}

		ch, err := r.peekRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return node, nil
			}
			return node, err
		}

		if isNewLine(ch) {
			r.discardRunes(1)
			return node, nil
		} else if ch == ';' {
			r.discardBytes(1)
			return node, nil
		} else if ch == '}' {
			return node, nil
		} else if ch == '{' {
			r.discardBytes(1)
			r.depth++
			children, err := readNodes(r)
			if err != nil {
				return node, err
			}
			r.depth--
			for i := range children {
				node.AddChild(children[i])
			}
		} else {
			err = readArgOrProp(r, &node)
			if err != nil {
				return node, err
			}
		}
	}
}

var errUnexpectedBareIdentifier = fmt.Errorf("%w: unexpected bare identifier", ErrInvalidSyntax)
var errUnexpectedTokenAfterIdentifier = fmt.Errorf("%w: unexpected token after identifier", ErrInvalidSyntax)
var errUnexpectedTokenAfterValue = fmt.Errorf("%w: unexpected token after value", ErrInvalidSyntax)

// readArgOrProp reads an argument or a property
// and adds them to the provided Node definition.
func readArgOrProp(r *reader, dest *Node) error {

	// A "slashdash" comment silences the whole argument or property
	slashdash, err := r.isNext(charsSlashDash[:])
	if err != nil {
		return err
	}
	if slashdash {
		r.discardBytes(2)
	}

	hint, err := readMaybeTypeHint(r)
	if err != nil {
		return err
	}

	// This can only be a property if there is no type hint at this time
	if hint == "" {
		i, err, quoted := readIdentifier(r, stopModeEquals)
		if err == nil {
			// Identifier read successfully.
			ch, err := r.peekRune()
			if errors.Is(err, io.EOF) {
				if quoted {
					if !slashdash {
						dest.AddArg(NewStringValue(string(i), ""))
					}
					return nil
				}
				return errUnexpectedBareIdentifier
			} else if err == nil {
				if isValidValueTerminator(ch) {
					if quoted {
						if !slashdash {
							dest.AddArg(NewStringValue(string(i), ""))
						}
						return nil
					}
					return errUnexpectedBareIdentifier
				} else if ch == '=' {
					r.discardBytes(1)
					v, err := readValue(r)
					if err != nil {
						return err
					}
					if !slashdash {
						dest.SetProp(i, v)
					}
					return nil
				}
				return errUnexpectedTokenAfterIdentifier
			}
			return err
		}

		// Else: Bad identifier. This should be a Value instead. Fallthrough.
	}

	v, err := readValue(r)
	if err != nil {
		// Not a valid Value
		return err
	}
	v.TypeHint = hint

	ch, err := r.peekRune()
	if err != nil {
		return err
	}
	if err == nil || errors.Is(err, io.EOF) || isValidValueTerminator(ch) {
		if !slashdash {
			dest.AddArg(v)
		}
		return nil
	}

	return errUnexpectedTokenAfterValue
}

// skipUntilNewLine discards the reader to the next new line character.
//
// If afterBreak is true, the reader is positioned after the newline break.
// If it is false, the reader is positioned just before a newline rune. (singular, in case of CRLF)
func skipUntilNewLine(r *reader, afterBreak bool) error {

	for {

		// CRLF is a special case as it spans two runes, so we check it first
		if isCrlf, err := r.isNext(charsCRLF[:]); isCrlf && err == nil {
			if afterBreak {
				r.discardBytes(2)
			} else {
				// Leave the LF only to simplify later checks
				r.discardBytes(1)
			}
			break
		}

		ch, err := r.peekRune()
		if err != nil {
			return err
		}

		if isNewLine(ch) {
			if afterBreak {
				r.discardBytes(1)
			}
			break
		}

		r.discardBytes(1)
	}

	return nil
}

// readUntilSignificant allows the provided reader to skip whitespace and comments.
//
// Note: this method will NOT skip over new lines.
func readUntilSignificant(r *reader) error {

outer:
	for {

		ch, err := r.peekRune()
		if err != nil {
			return err
		}

		if isWhitespace(ch) {
			r.discardBytes(1)
			continue
		}

		// Check for line continuation
		if ch == '\\' {
			r.discardBytes(1)
			if err := skipUntilNewLine(r, true); err != nil {
				return err
			}
			continue
		}

		// Check for single-line comments
		if comment, err := r.isNext(charsStartComment[:]); comment && err == nil {
			r.discardBytes(2)
			return skipUntilNewLine(r, false)
		}

		// Check for multiline comments
		if comment, err := r.isNext(charsStartCommentBlock[:]); comment && err == nil {
			r.discardBytes(2)
			// Per spec, multiline comments can be nested, so we can't do naive ReadString("*/")
			depth := 1
		inner:
			for {

				start, err := r.isNext(charsStartCommentBlock[:])
				if err != nil {
					return err
				}

				if start {
					depth += 1
					r.discardBytes(2)
					continue inner
				}

				end, err := r.isNext(charsEndCommentBlock[:])
				if err != nil {
					return err
				}

				if end {
					r.discardBytes(2)
					depth -= 1
					if depth <= 0 {
						continue outer
					} else {
						continue inner
					}
				}

				r.discardBytes(1)
			}
		}

		return nil
	}
}
