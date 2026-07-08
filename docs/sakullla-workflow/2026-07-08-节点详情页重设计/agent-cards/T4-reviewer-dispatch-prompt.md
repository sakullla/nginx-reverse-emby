# Sakullla Reviewer Dispatch Prompt

- reviewer_target_codex: sakullla-reviewer
- reviewer_target_claude_code: sakullla-dev:sakullla-reviewer
- review_type: code_review
- message_ref: docs/sakullla-workflow/2026-07-08-节点详情页重设计/agent-cards/T4-reviewer-message.yaml
- snapshot_ref: 6a82e35623629b597858e3893b2ea146a0423f51
- baseline_ref: 97835d398b270b07cd40e5f2221f167383faefbf
- diff_command: git --no-pager diff --no-ext-diff --no-textconv 97835d398b270b07cd40e5f2221f167383faefbf 6a82e35623629b597858e3893b2ea146a0423f51
- dispatch_state: ready
- dispatch_action: dispatch_by_message_ref
- file_ref_dispatch_ready: true
- workflow_refs_readable: true
- prompt_fallback_required: false
- prompt_envelope_injected: false
- fallback_dispatch_prompt_ready: false
- reviewer_passed: false

Dispatch the reviewer with the Message file ref and workflow refs listed above. Do not inline the complete Message envelope.
