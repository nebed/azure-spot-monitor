package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculatePriority(t *testing.T) {

	// Define sample node pools
	nodePools := NodepoolMap{
		"general":  {Name: "general", Discount: 0.5, EvictionRate: 0.2, Version: 2},
		"spotf_v2": {Name: "spotf", Discount: 0.4, EvictionRate: 0.05, Version: 2},
		"spotd_v2": {Name: "spotd", Discount: 0.6, EvictionRate: 0.2, Version: 2},
		"spotc_v2": {Name: "spotc", Discount: 0.5, EvictionRate: 0.2, Version: 2},
		"spota_v2": {Name: "spota", Discount: 0.9, EvictionRate: 0.15, Version: 2},
		"spotb_v2": {Name: "spotb", Discount: 0.9, EvictionRate: 0.05, Version: 2},
		"spote_v2": {Name: "spote", Discount: 0.9, EvictionRate: 0.3, Version: 2},
		"spotg_v2": {Name: "spotg", Discount: 0.9, EvictionRate: 0.3, Version: 2},
	}

	// Call the function
	priorities := calculatePriority(nodePools)

	// Assert the results
	expectedPriorities := map[int][]string{
		71: {".*general.*", ".*spotc.*"},
		72: {".*spotd.*"},
		79: {".*spota.*"},
		87: {".*spotb.*"},
		0:  {".*spote.*", ".*spotg.*", ".*spotf.*"},
	}
	if !reflect.DeepEqual(expectedPriorities, priorities) {
		assert.Equal(t, expectedPriorities, priorities)
	}
}

func TestVersions(t *testing.T) {

	// Define sample node pools
	nodePools := NodepoolMap{
		"general":  {Name: "general", Discount: 0.5, EvictionRate: 0.2, Version: 2},
		"spota_v2": {Name: "spota_v2", Discount: 0.9, EvictionRate: 0.2, Version: 2},
		"spota_v6": {Name: "spota_v6", Discount: 0.9, EvictionRate: 0.2, Version: 6},
		"spotb_v2": {Name: "spotb_v2", Discount: 0.9, EvictionRate: 0.05, Version: 2},
		"spotb_v6": {Name: "spotb_v6", Discount: 0.9, EvictionRate: 0.05, Version: 6},
	}

	// Call the function
	priorities := calculatePriority(nodePools)

	// Assert the results
	expectedPriorities := map[int][]string{
		71: {".*general.*"},
		75: {".*spota_v2.*"},
		79: {".*spota_v6.*"},
		87: {".*spotb_v2.*"},
		91: {".*spotb_v6.*"},
	}
	if !reflect.DeepEqual(expectedPriorities, priorities) {
		assert.Equal(t, expectedPriorities, priorities)
	}
}
