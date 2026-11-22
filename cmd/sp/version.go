package main

import (
	"context"
	"fmt"

	"github.com/Battleroid/snap-share/internal/version"
)

func init() {
	register(command{name: "version", summary: "print the version", run: func(context.Context, []string) error {
		fmt.Println(version.Version)
		return nil
	}})
}
