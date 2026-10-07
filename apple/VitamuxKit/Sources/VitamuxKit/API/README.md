The client generated from `api/openapi.yaml` ([J22.4](../../../../../docs/plan/E22-ios-app/J22.4-vitamuxkit-client.md)). The build plugin generates `Client`, `Components` and `Operations` at every build from `openapi.yaml` (the ingest tag, HealthBridgeKit's, is filtered out in `openapi-generator-config.yaml`); no generated Swift is committed.

`openapi.yaml` is a copy, not a symlink: `make openapi` runs `../../../copy-openapi.sh`, which rewrites the nullable references the generator would drop, and CI fails when the copy drifts. Never edit it by hand.

`Client+Profile.swift` builds a `Client` for a `ServerProfile` with the one `SessionMiddleware` (`Core/`). `xcodebuild` needs `-skipPackagePluginValidation` (or "Trust & Enable" once in Xcode) to run the plugin.
