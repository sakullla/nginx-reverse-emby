const ROLE_IDS = ['administrator', 'operator', 'readonly']
const RESOURCE_KINDS = ['agent', 'http_rule', 'l4_rule', 'relay_listener', 'certificate', 'egress_profile']

export function previewRoles() {
  return [
    { id: 'administrator', name: '管理员' },
    { id: 'operator', name: '运维' },
    { id: 'readonly', name: '只读' }
  ]
}

export function previewUsers(count = 200) {
  const size = Math.max(2, Number(count) || 200)
  const users = [
    { id: 'usr-admin', username: 'alice', display_name: 'Alice', disabled: false, role_ids: ['administrator'] },
    { id: 'usr-ops', username: 'bob', display_name: 'Bob', disabled: false, role_ids: ['operator'] }
  ]
  for (let index = 3; index <= size; index += 1) {
    const n = String(index).padStart(3, '0')
    users.push({
      id: `usr-${n}`,
      username: `user${n}`,
      display_name: `用户 ${n}`,
      disabled: index % 17 === 0,
      role_ids: [ROLE_IDS[index % ROLE_IDS.length]]
    })
  }
  return users
}

export function previewMembers(count = 200, groupID = 'default') {
  const members = Object.fromEntries(RESOURCE_KINDS.map((kind) => [kind, []]))
  const size = Math.max(0, Number(count) || 0)
  for (let index = 1; index <= size; index += 1) {
    const kind = RESOURCE_KINDS[(index - 1) % RESOURCE_KINDS.length]
    const id = `${kind}-${index}`
    members[kind].push({
      id,
      name: kind === 'http_rule' ? `https://host-${index}.example.com` : id,
      resource_kind: kind,
      resource_group_id: groupID,
      context: index % 2 === 0 ? 'edge-1' : 'local'
    })
  }
  return members
}

export function previewGrants(users, groupID = 'default') {
  const grants = ROLE_IDS.map((id) => ({
    subject_kind: 'role',
    subject_id: id,
    resource_group_id: groupID
  }))
  for (const user of users) {
    grants.push({
      subject_kind: 'user',
      subject_id: user.id,
      resource_group_id: groupID
    })
  }
  return grants
}

export function previewResourceGroups({ userCount = 200, memberCount = 200 } = {}) {
  const users = previewUsers(userCount)
  const roles = previewRoles()
  const defaultMembers = previewMembers(memberCount, 'default')
  const defaultGrants = previewGrants(users, 'default')
  const teamMembers = previewMembers(24, 'team')
  const teamGrants = previewGrants(users.slice(0, 12), 'team')
  const groups = [
    {
      id: 'default',
      name: '默认组',
      description: '未分组资源',
      builtin: true,
      grant_count: defaultGrants.length,
      resource_count: memberCount,
      grants: defaultGrants,
      members: defaultMembers
    },
    {
      id: 'team',
      name: '团队组',
      description: '团队可见资源',
      builtin: false,
      grant_count: teamGrants.length,
      resource_count: 24,
      grants: teamGrants,
      members: teamMembers
    }
  ]
  return { users, roles, groups, grants: [...defaultGrants, ...teamGrants] }
}
