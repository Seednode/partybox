/*
Copyright © 2026 Seednode <seednode@seedno.de>
*/

package main

import (
	"log"

	"github.com/spf13/cobra"
)

const (
	releaseVersion = "0.12.0"
)

func main() {
	log.SetFlags(0)
	cfg := &Config{}
	cobra.CheckErr(newCmd(cfg).Execute())
}
