package main

import (
	"os"
	"strings"

	"github.com/CytonicMC/Cynder/cynder"
	"go.minekube.com/gate/cmd/gate"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

// It's a normal Go program, we only need
// to register our plugins and execute Gate.
func main() {

	proxy.Plugins = append(proxy.Plugins, cynder.Plugin)
	// Simply execute Gate as if it was a normal Go program.
	// Gate will take care of everything else for us,
	// such as config auto-reloading and flags like --debug.

	replaceConfigs()

	gate.Execute()
}

func replaceConfigs() {
	data, err := os.ReadFile("config.yml")
	if err != nil {
		panic(err)
	}

	port := os.Getenv("CYNDER_PORT")
	if port == "" {
		panic("CYNDER_PORT not set")
	}

	updated := strings.ReplaceAll(string(data), "${PORT}", port)

	if err := os.WriteFile("config.yml", []byte(updated), 0644); err != nil {
		panic(err)
	}
}
