# Refactor Agents Page with Global Search and Remove Version Policy from Nav

## Goal
1. Refactor the Agents page (`/agents`) to match the card-grid + header-search pattern used by RulesPage.
2. Add local search on AgentsPage to filter the node list by name, IP, or tag.
3. Extend the existing `GlobalSearch` component so it can also search across agents.
4. Remove "版本策略" from the sidebar and bottom navigation while keeping the route/page alive.

## Current State
- `AgentsPage.vue` renders a vertical list of `.agent-card` rows without search.
- `RulesPage.vue` uses a `.rule-grid` card grid and a `.search-wrapper` in the header.
- `GlobalSearch.vue` searches rules, L4 rules, and certificates across all agents.
- `Sidebar.vue` and `BottomNav.vue` both contain a link to `/versions` labeled "版本策略".

## Design

### 1. AgentsPage Refactor
- **Layout**: Switch from `max-width: 800px` vertical list to `max-width: 1200px` card grid (`grid-template-columns: repeat(auto-fill, minmax(280px, 1fr))`).
- **Header**: Add a `.search-wrapper` identical to RulesPage (search icon, input, clear button) positioned between the subtitle and the action buttons.
- **Search logic**:
  - Filter `agents` by `searchQuery`.
  - Match against: `agent.name`, `agent.agent_url`, `agent.last_seen_ip`, and `agent.tags`.
  - Support `#id=...` exact-match syntax, same as RulesPage.
  - Empty state when no matches: reuse the existing empty/loading visuals.
- **Card content** (per card):
  - Top row: status badge + mode badge.
  - Name + URL/IP.
  - Stats row: HTTP count, L4 count, last-seen time.
  - Action row: rename + delete buttons.
  - Clicking the card navigates to `/agents/${agent.id}`.
- **Modals**: Keep existing join-node, rename, and delete modals unchanged.
- **Styling**: Follow the border-radius, colors, and shadow conventions from RulesPage cards.

### 2. GlobalSearch Extension
- **New result type**: `agent`.
- **Matching**: when the user types in `GlobalSearch`, also filter the agents list by `name`, `agent_url`, and `last_seen_ip`.
- **Result item UI**: show a small "节点" type badge, agent name, and its URL/IP.
- **Navigation**: clicking an agent result routes to `/agents/${agent.id}`.
- **Grouping**: agent results appear in a single group (or merged per agent if we later expand this); for simplicity, list each matched agent as its own result item under a "节点" grouping header.

### 3. Navigation Changes
- **Sidebar.vue**: Remove the `<RouterLink to="/versions">...</RouterLink>` block from both expanded and collapsed nav sections.
- **BottomNav.vue**: Remove the "版本策略" entry from the "更多" dropdown (it is not in the main bottom icons, so only the dropdown is affected if present; currently it is not present there, but verify no reference exists).
- **Router**: Do NOT delete the `/versions` route or `VersionsPage.vue`. Direct navigation and bookmarks must continue to work.

## Files to Modify
1. `panel/frontend/src/pages/AgentsPage.vue`
2. `panel/frontend/src/components/GlobalSearch.vue`
3. `panel/frontend/src/components/layout/Sidebar.vue`
4. `panel/frontend/src/components/layout/BottomNav.vue` (verify and clean if needed)

## Testing Checklist
- [ ] AgentsPage shows card grid and local search filters correctly.
- [ ] `#id=...` search syntax works on AgentsPage.
- [ ] GlobalSearch includes agent results and navigates to agent detail.
- [ ] Sidebar no longer shows "版本策略".
- [ ] `/versions` URL still loads the page when entered directly.
