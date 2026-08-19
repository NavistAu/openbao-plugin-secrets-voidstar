---
name: Bug report
about: Report a problem with the plugin
title: ""
labels: bug
---

## Plugin version

<!-- The release tag you downloaded, or the commit you built from. If the
problem happens during `bao plugin register` or `bao secrets enable` (the
plugin is not yet mounted), report the release tag here instead of a
mount-derived version string. -->

## OpenBao version

<!-- Output of `bao version`. -->

## Mount configuration

<!-- Output of `bao read <mount>/admin/config` (the engine config path).
Redact `secret_id` and `role_id` — they are not returned by this command
by default, but double-check before pasting. -->

## Status output

<!-- Output of `bao read <mount>/admin/status` (the engine status path) —
loopback client health, mapping count, failure counters, and any
quarantined mappings. This is the primary triage surface for dereference
failures. It can include view and target-mount names; redact target
values if the path structure itself is sensitive. -->

## View / mapping

<!-- The view path involved and, if relevant, its target
(`bao read <mount>/admin/map/<view>`). Field names and path structure are
fine to include as-is; redact any secret material a target path or
`#field` suffix might reveal about your layout if you'd rather not share
it. -->

## Failure class (if applicable)

<!-- If this is a dereference failure, which one did you see:
upstream_read_failure, missing_field, lease_violation, or
quarantined_fastfail? Include the exact error message returned. -->

## Loopback AppRole

<!-- If this involves loopback authentication: `approle_mount`, whether
the AppRole's secret_id was created with secret_id_num_uses=0 and
secret_id_ttl=0, and `loopback.client_connected` /
`loopback.init_last_error` from the status output above. -->

## target_mount_allowlist (if applicable)

<!-- If this is a target-mount rejection: the configured
target_mount_allowlist value and the target mount name that was
rejected. -->

## Expected behavior

<!-- What you expected to happen. -->

## Actual behavior

<!-- What actually happened. -->

## Relevant log lines

<!-- OpenBao server log lines around the failure. Redact secrets. -->

## Additional context

<!-- Anything else that might help: steps to reproduce, whether this is new
or a regression, related configuration. -->
