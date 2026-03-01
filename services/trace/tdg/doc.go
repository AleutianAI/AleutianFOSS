// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package tdg provides Test-Driven Generation (TDG) for Trace.
//
// TDG is an agent mode that forces correctness proofs through tests:
//
//  1. UNDERSTAND - Agent analyzes the bug/feature request
//  2. WRITE_TEST - Agent writes a test that should FAIL on current code
//  3. VERIFY_FAIL - System runs test, must fail (proves bug exists)
//  4. WRITE_FIX - Agent implements the fix
//  5. VERIFY_PASS - System runs test, must pass (proves fix works)
//  6. REGRESSION - System runs full suite (proves no breakage)
//  7. DONE - Fix is proven correct
//
// The TDG controller operates as an internal state machine, separate from
// the main agent loop states. It runs within the agent's EXECUTE phase
// when TDG mode is requested.
//
// # Multi-Language Support
//
// TDG supports multiple languages through a configuration registry:
//   - Go: go test -v -run {name}
//   - Python: pytest -v -k {name}
//   - TypeScript: npx jest --testNamePattern {name}
//
// # Iteration Limits
//
// To prevent infinite loops, TDG enforces retry limits:
//   - Max test generation attempts: 3
//   - Max fix attempts: 5
//   - Max regression fix attempts: 3
//
// When limits are exceeded, TDG returns an error for user escalation.
//
// # Thread Safety
//
// Controller instances are NOT safe for concurrent use. Each TDG session
// should use its own Controller instance. The LanguageConfigRegistry is
// safe for concurrent reads after initialization.
//
// # Example Usage
//
//	cfg := tdg.DefaultConfig()
//	runner := tdg.NewTestRunner(cfg, logger)
//	files := tdg.NewFileManager(projectRoot, logger)
//	gen := tdg.NewTestGenerator(llm, contextAsm, logger)
//
//	controller := tdg.NewController(cfg, runner, files, gen, logger)
//
//	result, err := controller.Run(ctx, &tdg.Request{
//	    BugDescription: "ValidateToken crashes when claims is nil",
//	    ProjectRoot:    "/path/to/project",
//	    Language:       "go",
//	})
//
//	if result.Success {
//	    fmt.Printf("Fixed! Test: %s\n", result.ReproducerTest.Name)
//	}
package tdg
