package main

import (
	"log"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui"
)

var version = "dev"

func main() {
	if err := ui.NewDesktop(product.Current(version)).Run(); err != nil {
		log.Fatal(err)
	}
}
