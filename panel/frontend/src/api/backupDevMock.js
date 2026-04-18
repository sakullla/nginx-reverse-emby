export function createBackupImportDevMock() {
  return {
    ok: true,
    manifest: {
      package_version: 1,
      source_architecture: 'main-legacy',
      exported_at: new Date().toISOString(),
      includes_certificates: true,
      counts: {
        agents: 1,
        http_rules: 2,
        l4_rules: 1,
        relay_listeners: 0,
        certificates: 1,
        version_policies: 0
      }
    },
    summary: {
      imported: 2,
      skipped_conflict: 1,
      skipped_invalid: 0,
      skipped_missing_material: 0
    },
    report: {
      imported: [
        { resource: 'agent', identifier: 'edge-01', detail: 'agent imported' },
        { resource: 'http_rule', identifier: 'https://media.example.com', detail: 'rule imported' }
      ],
      skipped_conflict: [
        { resource: 'http_rule', identifier: 'https://exists.example.com', detail: 'frontend_url already exists' }
      ],
      skipped_invalid: [],
      skipped_missing_material: []
    }
  }
}
