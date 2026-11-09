# AI Disclosure

**TLDR:** <br>
AI is a tool, not a medium or decision maker. <br>
It is allowed to support you but it's not there to do the work for you. <br>
You are responsible for you own code and you will be blocked if you submit AI generated pull requests and can't answer questions about it.

## Overview

VRC-Haven is an open-source Go (GoLang) project developed with limited assistance from artificial intelligence (AI) tools.

AI has been used as a development aid to support code quality, troubleshooting, testing, and documentation. All AI-assisted contributions are reviewed by a human before being incorporated into the project.

**AI is not used by VRC-Haven at runtime.**<br>
The application does not depend on AI models, AI APIs, or external AI services to operate.

## AI Tools Used

The following AI tool has been used during development:

- Claude (Anthropic)

No specific model version is declared, as model versions may vary between development sessions.

## How AI Is Used

AI assistance has been limited to the following development activities:

- **Code improvements and refactoring:** Suggesting improvements to existing Go code, readability, maintainability, and structure.
- **Debugging and troubleshooting:** Helping investigate potential bugs, understand errors, and explore possible solutions.
- **Testing:** Assisting with the creation and improvement of Go unit tests.
- **Documentation:** Assisting with writing and improving project documentation, including the README.

AI-generated suggestions are treated as recommendations rather than authoritative solutions.

## Human Oversight and Verification

Human oversight is maintained throughout the development process.

- All AI-assisted code is reviewed and understood by a human before being accepted.
- AI-generated suggestions are evaluated for correctness, relevance, and suitability.
- Tests and other validation methods are used to help verify changes.
- AI-generated output is not accepted blindly or treated as inherently correct.

The project uses several verification practices, including:

- Go unit tests
- Manual testing
- Static analysis and linting
- Automated CI/CD (Runner) checks

These practices help maintain code quality and identify potential issues. However, they do not guarantee that all defects or security vulnerabilities will be detected.

## Runtime AI Usage

VRC-Haven does not use AI as part of its runtime functionality.

Specifically:

- No AI models are embedded in or required by the application.
- No AI APIs or external AI inference services are required.
- No runtime AI-generated decisions or content are produced by the application.

The use of AI described in this document relates exclusively to the software development process.

## Contributor Policy

AI-assisted contributions from external contributors are permitted, subject to maintainer approval.

Contributors must:

- Review and understand any AI-generated code they submit.
- Verify that submitted code behaves as intended.
- Ensure that contributions comply with the project's licensing and contribution requirements.
- Remain accountable for the correctness, quality, and originality of their contributions.

Maintainers may request clarification about AI involvement where necessary.

## Responsibility and Accountability

Responsibility for the project is shared between its maintainers and contributors.

AI tools are not considered project contributors, maintainers, or decision-makers.

Humans remain responsible for reviewing, approving, maintaining, and supporting the codebase.

## Transparency and Updates

This disclosure is intended to provide transparency about the role of AI in the development of VRC-Haven.

As the project evolves, its AI usage and development practices may change. This document will be updated when those changes materially affect how AI is used in the project.

Last updated: November 2026.
