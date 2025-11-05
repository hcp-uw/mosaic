package main

import (
	"encoding/json"
	"os"
)

func main() {

	file, err := NormPath("config.json")
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		panic(err)
	}

	tier, ok := config["tier"].(float64)
	if !ok {
		panic("Invalid tier value")
	}

	switch int(tier) {
		case 0:
			T0_Main()
		case 1:
			T1_Main()
		default:
			panic("Unknown tier")
	}

}