# Sakullla Reviewer Dispatch Prompt

- reviewer_target_codex: sakullla-reviewer
- reviewer_target_claude_code: sakullla-dev:sakullla-reviewer
- review_type: code_review
- message_ref: docs/sakullla-workflow/2026-07-08-节点详情页重设计/agent-cards/T5-reviewer-message.yaml
- snapshot_ref: 34268713823e8d55a88645b60c70a2cbcee9000b
- baseline_ref: aa7631c60af216d85e0cc8bc3105651e2ca65872
- diff_command: git --no-pager diff --no-ext-diff --no-textconv aa7631c60af216d85e0cc8bc3105651e2ca65872 34268713823e8d55a88645b60c70a2cbcee9000b
- dispatch_state: ready
- dispatch_action: dispatch_by_message_ref
- file_ref_dispatch_ready: true
- workflow_refs_readable: true
- prompt_fallback_required: false
- prompt_envelope_injected: false
- fallback_dispatch_prompt_ready: false
- reviewer_passed: false

Dispatch the reviewer with the Message file ref and workflow refs listed above. Do not inline the complete Message envelope.
