package query

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// MaxActionBody is the longest body a shortcut may have, in UTF-16 code
// units — a letter of any alphabet is one, an emoji two — so that it always
// fits a Telegram caption.
const MaxActionBody = 900

// ActionBodyLen is how long a shortcut's body is, as the API measures it.
func ActionBodyLen(body string) int {
	n := 0
	for _, r := range body {
		n += utf16.RuneLen(r)
	}
	return n
}

// CheckActionBody refuses a body the API would refuse for its length or for
// a NUL, before it is sent. Which placeholders it may carry is the API's to
// say.
func CheckActionBody(body string) error {
	if strings.ContainsRune(body, 0) {
		return errors.New("the body carries a NUL character, which a shortcut never may")
	}
	if n := ActionBodyLen(body); n > MaxActionBody {
		return fmt.Errorf("the body is %d characters long and a shortcut holds %d at most, an emoji counting as two", n, MaxActionBody)
	}
	return nil
}
