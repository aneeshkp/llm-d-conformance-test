package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadProfile reads a test profile YAML file.
func LoadProfile(path string) (*TestProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile %s: %w", path, err)
	}
	var profile TestProfile
	if err := yaml.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parsing profile %s: %w", path, err)
	}
	return &profile, nil
}

// LoadTestCase reads a single test case YAML file.
func LoadTestCase(path string) (*TestCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading test case %s: %w", path, err)
	}
	var tc TestCase
	if err := yaml.Unmarshal(data, &tc); err != nil {
		return nil, fmt.Errorf("parsing test case %s: %w", path, err)
	}
	return &tc, nil
}

// LoadTestCasesFromDir loads all YAML test cases from a directory.
func LoadTestCasesFromDir(dir string) ([]*TestCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading test case directory %s: %w", dir, err)
	}
	cases := make([]*TestCase, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !isYAMLFile(entry.Name()) {
			continue
		}
		tc, err := LoadTestCase(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		cases = append(cases, tc)
	}
	return cases, nil
}

// FilterTestCasesByNames returns only test cases whose names match the provided list.
func FilterTestCasesByNames(cases []*TestCase, names []string) []*TestCase {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	var filtered []*TestCase
	for _, tc := range cases {
		if nameSet[tc.Name] {
			filtered = append(filtered, tc)
		}
	}
	return filtered
}

// FilterTestCasesByLabels returns test cases that have at least one of the provided labels.
func FilterTestCasesByLabels(cases []*TestCase, labels []string) []*TestCase {
	labelSet := make(map[string]bool, len(labels))
	for _, l := range labels {
		labelSet[l] = true
	}
	var filtered []*TestCase
	for _, tc := range cases {
		for _, l := range tc.Labels {
			if labelSet[l] {
				filtered = append(filtered, tc)
				break
			}
		}
	}
	return filtered
}

// ResolveProfileTestCases loads and filters test cases for a given profile.
func ResolveProfileTestCases(profile *TestProfile, testCaseDir string) ([]*TestCase, error) {
	allCases, err := LoadTestCasesFromDir(testCaseDir)
	if err != nil {
		return nil, err
	}
	if len(profile.TestCases) > 0 {
		return FilterTestCasesByNames(allCases, profile.TestCases), nil
	}
	if len(profile.Labels) > 0 {
		return FilterTestCasesByLabels(allCases, profile.Labels), nil
	}
	return allCases, nil
}

func isYAMLFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}
