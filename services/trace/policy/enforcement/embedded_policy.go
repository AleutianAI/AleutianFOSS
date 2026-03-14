// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

/*
This file serves as the bridge between the build system and the runtime logic. It utilizes the Go
embed package to bake the data_classification_patterns.yaml file directly into the compiled binary.
This ensures that the policy rules are immutable at runtime and travel with the executable.
*/

package enforcement

import (
	_ "embed"
)

// DataClassificationPatterns holds the raw byte content of the 'data_classification_patterns.yaml' file.
//
// This variable is populated at compile-time using the Go 'embed' directive. By baking the
// YAML directly into the binary, we ensure that the security policies are immutable and
// cannot be tampered with on the host filesystem without recompiling the application.
//
// Usage:
//
//	// Pass these bytes directly to yaml.Unmarshal
//	err := yaml.Unmarshal(enforcement.DataClassificationPatterns, &targetStruct)
//
//go:embed data_classification_patterns.yaml
var DataClassificationPatterns []byte
