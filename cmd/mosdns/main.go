package main

import (
	"fmt"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/IrineSistiana/mosdns/v5/mlog"
	_ "github.com/IrineSistiana/mosdns/v5/plugin"
	_ "github.com/IrineSistiana/mosdns/v5/tools"
	_ "github.com/hyird/GuardDNS/internal/circuitplugin"
	_ "github.com/hyird/GuardDNS/internal/supervisorplugin"
	"github.com/spf13/cobra"
)

var version = "v5.3.4-guarddns"

func init() {
	coremain.AddSubCmd(&cobra.Command{
		Use:   "version",
		Short: "Print version and exit.",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(version)
		},
	})
}

func main() {
	if err := coremain.Run(); err != nil {
		mlog.S().Fatal(err)
	}
}
