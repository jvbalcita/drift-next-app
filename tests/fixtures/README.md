# Sanitized Phase 9 and 10.5 fixtures

These fixtures contain bounded metadata only. They deliberately contain no
screenshots, UI trees, OCR payloads, credentials, device connections, or model
provider responses. The fixture tests verify JSON validity and redaction before
the files can be used by later workflow and recorder tests. Workflow fixtures
describe fake target sets, typed actions, replay compatibility, and postconditions
without carrying raw coordinates or runtime secrets.

Recording fixtures describe fake-device BEFORE/ACTION/AFTER metadata and
explicit optional-enrichment omission. They contain no screenshot, UI-tree,
credential, device-connection, or model-provider bytes.
