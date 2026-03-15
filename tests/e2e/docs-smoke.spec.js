const { test, expect } = require('@playwright/test')
const fs = require('fs')
const path = require('path')

const repoRoot = path.resolve(__dirname, '..', '..')

function read(file) {
  return fs.readFileSync(path.join(repoRoot, file), 'utf8')
}

test('README and AGENT_EXAMPLES contain lightweight master-agent guidance', async () => {
  const readme = read('README.md')
  const examples = read('AGENT_EXAMPLES.md')

  expect(readme).toContain('Master / Agent')
  expect(readme).toContain('NAT Agent')
  expect(readme).toContain('join-agent.sh')
  expect(readme).toContain('--install-systemd')

  expect(examples).toContain('# Master / Agent')
  expect(examples).toContain('NAT')
  expect(examples).toContain('APPLY_COMMAND')
})

test('example files stay aligned with join-agent script arguments', async () => {
  const joinScript = read('scripts/join-agent.sh')
  const envExample = read('examples/light-agent.env.example')
  const serviceExample = read('examples/light-agent.service.example')

  for (const flag of ['--master-url', '--register-token', '--agent-name', '--agent-token', '--agent-url', '--data-dir', '--rules-file', '--state-file', '--interval-ms', '--version', '--tags', '--apply-command', '--install-systemd']) {
    expect(joinScript).toContain(flag)
  }

  for (const key of ['MASTER_PANEL_URL', 'MASTER_REGISTER_TOKEN', 'AGENT_NAME', 'AGENT_TOKEN', 'AGENT_PUBLIC_URL', 'AGENT_VERSION', 'AGENT_TAGS', 'AGENT_HEARTBEAT_INTERVAL_MS', 'RULES_JSON', 'AGENT_STATE_FILE', 'APPLY_COMMAND']) {
    expect(envExample).toContain(key)
  }

  expect(serviceExample).toContain('EnvironmentFile=')
  expect(serviceExample).toContain('light-agent.js')
  expect(serviceExample).toContain('Restart=always')
})
