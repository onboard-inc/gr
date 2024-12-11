package main

import (
	"fmt"

	"golang.org/x/crypto/openpgp"
)

func main() {
	_ = openpgp.ReadArmoredKeyRing
	fmt.Println("Hello world!")
}
