// Package demo shows a little of everything.
package demo

import (
	"fmt"
	"strings"
)

/* Point is a 2D point. */
type Point struct {
	X, Y int
}

const limit = 0x1F

func (p Point) String() string {
	if p.X > limit && p.Y != 0 {
		return fmt.Sprintf("(%d, %d)\n", p.X, p.Y)
	}
	return strings.Repeat("<&>", 2) + `raw "quoted"`
}

var ok = true && !false || nil == nil
