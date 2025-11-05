package main

import (
	"fmt"
	// "github.com/hcp-uw/mosaic/networking/utils"
)

func T0_Main() {

	domains, err := GetTier1("tier1.txt")
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	TIER1, err := FindFastest(domains)
	if err != nil {
		fmt.Printf("Error finding fastest domain: %v\n", err)
		return
	} else {
		fmt.Printf("Fastest domain: %s\n", TIER1)
	}

}

