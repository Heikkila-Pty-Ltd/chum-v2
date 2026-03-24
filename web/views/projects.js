/* ============================================================
   Projects — "Per-project detail + portfolio view"
   Health cards + drill-in task tree.
   Uses Alpine.js for declarative reactivity.
   ============================================================ */
(() => {
  const TERMINAL = new Set(['completed', 'done']);
  const FAILED = new Set([
    'failed', 'dod_failed', 'rejected', 'quarantined', 'budget_exceeded',
  ]);

  document.addEventListener('alpine:init', () => {
    Alpine.data('projectsPage', () => ({
      // State
      loading: true,
      error: null,
      projects: [],
      cards: {},          // name -> { stats, overview, health, loading }
      detail: null,       // null = list view, string = project name
      detailLoading: false,
      tree: [],           // flat tree items for detail view
      treeCollapsed: {},  // id -> true
      plans: [],          // plan list for detail view
      healthData: null,

      // Lifecycle
      async init() {
        await this.load();
      },

      async load() {
        this.loading = true;
        this.error = null;
        try {
          const [projectsRes, healthRes] = await Promise.allSettled([
            App.API.projects(),
            App.API.health(),
          ]);
          if (projectsRes.status === 'fulfilled') {
            this.projects = projectsRes.value.projects || [];
          }
          if (healthRes.status === 'fulfilled') {
            this.healthData = healthRes.value;
          }

          // Fetch stats + overview-grouped for each project
          await this.loadCards();
        } catch (err) {
          this.error = err.message;
        }
        this.loading = false;
      },

      async loadCards() {
        const fetches = this.projects.map(async (p) => {
          const [statsRes, overviewRes] = await Promise.allSettled([
            App.API.stats(p),
            App.API.overviewGrouped(p),
          ]);
          const stats = statsRes.status === 'fulfilled' ? statsRes.value : null;
          const overview = overviewRes.status === 'fulfilled' ? overviewRes.value : null;
          this.cards[p] = this.buildCard(p, stats, overview);
        });
        await Promise.allSettled(fetches);
      },

      buildCard(name, stats, overview) {
        const byStatus = (stats && stats.by_status) || {};
        const total = (stats && stats.total) || 0;
        const completed = (byStatus.completed || 0) + (byStatus.done || 0);
        const failed = Object.keys(byStatus)
          .filter(s => FAILED.has(s))
          .reduce((sum, s) => sum + byStatus[s], 0);
        const running = byStatus.running || 0;
        const blocked = byStatus.needs_review || 0;
        const pct = total > 0 ? Math.round((completed / total) * 100) : 0;

        // Health assessment
        let health = 'healthy';
        let healthLabel = 'Healthy';
        if (total > 0) {
          const failPct = failed / total;
          if (failPct > 0.3) {
            health = 'failing';
            healthLabel = 'Failing';
          } else if (failed > 0) {
            health = 'degraded';
            healthLabel = 'Degraded';
          }
        }

        // Velocity from overview-grouped (count completed in 24h/7d)
        let velocity24h = 0;
        let velocity7d = 0;
        if (overview && overview.velocity) {
          velocity24h = overview.velocity.completed_24h || 0;
          velocity7d = overview.velocity.completed_7d || 0;
        }

        return {
          name, total, completed, failed, running, blocked, pct,
          health, healthLabel, velocity24h, velocity7d,
          byStatus,
        };
      },

      get sortedProjects() {
        // Unhealthy projects first
        const order = { failing: 0, degraded: 1, healthy: 2 };
        return [...this.projects].sort((a, b) => {
          const ca = this.cards[a];
          const cb = this.cards[b];
          if (!ca || !cb) return 0;
          return (order[ca.health] || 2) - (order[cb.health] || 2);
        });
      },

      // Status bar segments
      statusSegments(card) {
        if (!card || card.total === 0) return [];
        const segments = [];
        const statuses = ['completed', 'done', 'running', 'ready', 'open',
          'failed', 'dod_failed', 'quarantined', 'budget_exceeded',
          'needs_refinement', 'needs_review', 'rejected', 'stale', 'paused', 'decomposed'];
        for (const s of statuses) {
          const count = card.byStatus[s] || 0;
          if (count > 0) {
            segments.push({
              status: s,
              pct: (count / card.total) * 100,
              color: App.statusColor(s),
              count,
              label: s.replace(/_/g, ' '),
            });
          }
        }
        return segments;
      },

      healthShape(health) {
        if (health === 'failing') return '\u25CF'; // filled circle
        if (health === 'degraded') return '\u25B2'; // triangle
        return '\u25CB'; // open circle
      },

      healthColor(health) {
        if (health === 'failing') return App.statusColor('failed');
        if (health === 'degraded') return App.statusColor('dod_failed');
        return App.statusColor('completed');
      },

      // --- Detail view ---

      async openDetail(name) {
        this.detail = name;
        this.detailLoading = true;
        this.treeCollapsed = {};
        this.plans = [];

        try {
          const [treeRes, plansRes] = await Promise.allSettled([
            App.API.tree(name),
            App.API.plans(name),
          ]);

          if (treeRes.status === 'fulfilled') {
            this.tree = this.buildFlatTree(treeRes.value.nodes || []);
          }
          if (plansRes.status === 'fulfilled') {
            this.plans = plansRes.value.plans || [];
          }
        } catch { /* silent */ }
        this.detailLoading = false;
      },

      closeDetail() {
        this.detail = null;
        this.tree = [];
        this.plans = [];
      },

      // Build flat tree with depth from parent_id relationships
      buildFlatTree(nodes) {
        const byId = {};
        const childMap = {};
        for (const n of nodes) {
          byId[n.id] = n;
          if (n.parent_id) {
            if (!childMap[n.parent_id]) childMap[n.parent_id] = [];
            childMap[n.parent_id].push(n.id);
          }
        }

        const result = [];
        const visited = new Set();

        const walk = (id, depth) => {
          if (visited.has(id)) return;
          visited.add(id);
          const node = byId[id];
          if (!node) return;
          result.push({ ...node, depth, hasChildren: !!(childMap[id] && childMap[id].length > 0) });
          if (childMap[id]) {
            for (const cid of childMap[id]) {
              walk(cid, depth + 1);
            }
          }
        };

        // Roots: nodes without parent or whose parent isn't in this set
        for (const n of nodes) {
          if (!n.parent_id || !byId[n.parent_id]) {
            walk(n.id, 0);
          }
        }

        // Any unvisited (cycles or orphans)
        for (const n of nodes) {
          if (!visited.has(n.id)) {
            result.push({ ...n, depth: 0, hasChildren: false });
          }
        }

        return result;
      },

      isTreeVisible(item) {
        // Walk ancestors — if any ancestor is collapsed, this item is hidden
        if (item.depth === 0) return true;
        let parentId = item.parent_id;
        while (parentId) {
          if (this.treeCollapsed[parentId]) return false;
          const parent = this.tree.find(t => t.id === parentId);
          parentId = parent ? parent.parent_id : null;
        }
        return true;
      },

      toggleCollapse(id) {
        this.treeCollapsed[id] = !this.treeCollapsed[id];
      },

      // --- Helpers ---
      async refresh() {
        if (this.detail) {
          await this.openDetail(this.detail);
        } else {
          await this.loadCards();
        }
      },

      timeAgo(ts) { return App.timeAgo(ts); },
      statusColor(s) { return App.statusColor(s); },
      truncate(s, n) { return App.truncate(s, n || 80); },
    }));
  });

  function render(viewport, project) {
    viewport.innerHTML = `
    <div x-data="projectsPage" class="projects-page view-enter">
      <!-- Loading -->
      <template x-if="loading">
        <div class="loading-state">loading\u2026</div>
      </template>

      <!-- Error -->
      <template x-if="error && !loading">
        <div class="empty-state">
          Failed to load projects
          <div class="empty-state-hint" x-text="error"></div>
        </div>
      </template>

      <!-- List View -->
      <template x-if="!loading && !error && !detail">
        <div>
          <template x-if="projects.length === 0">
            <div class="empty-state">No projects</div>
          </template>

          <div class="proj-grid">
            <template x-for="p in sortedProjects" :key="p">
              <div class="proj-card" :class="'proj-card--' + (cards[p] ? cards[p].health : 'healthy')"
                   @click="openDetail(p)">
                <div class="proj-card-header">
                  <span class="proj-card-name" x-text="p"></span>
                  <span class="proj-health-indicator" :style="'color:' + healthColor((cards[p] || {}).health || 'healthy')">
                    <span x-text="healthShape((cards[p] || {}).health || 'healthy')"></span>
                    <span class="proj-health-label" x-text="(cards[p] || {}).healthLabel || 'Healthy'"></span>
                  </span>
                </div>

                <!-- Progress bar -->
                <div class="proj-pct-row">
                  <span class="proj-pct-value" x-text="(cards[p] || {}).pct + '%'"></span>
                  <span class="proj-pct-label">complete</span>
                  <span class="proj-pct-detail" x-text="(cards[p] || {}).completed + '/' + (cards[p] || {}).total"></span>
                </div>

                <!-- Status bar -->
                <div class="proj-status-bar">
                  <template x-for="seg in statusSegments(cards[p])" :key="seg.status">
                    <div class="proj-status-segment"
                         :style="'width:' + seg.pct + '%; background:' + seg.color"
                         :title="seg.label + ': ' + seg.count"></div>
                  </template>
                </div>

                <!-- Stats row -->
                <div class="proj-stats-row">
                  <span class="proj-stat" x-show="(cards[p] || {}).running > 0">
                    <span class="proj-stat-num" x-text="(cards[p] || {}).running"></span> running
                  </span>
                  <span class="proj-stat" x-show="(cards[p] || {}).failed > 0">
                    <span class="proj-stat-num proj-stat-num--fail" x-text="(cards[p] || {}).failed"></span> failed
                  </span>
                  <span class="proj-stat" x-show="(cards[p] || {}).blocked > 0">
                    <span class="proj-stat-num" x-text="(cards[p] || {}).blocked"></span> blocked
                  </span>
                  <span class="proj-stat" x-show="(cards[p] || {}).velocity24h > 0">
                    <span class="proj-stat-num" x-text="(cards[p] || {}).velocity24h"></span>/24h
                  </span>
                </div>
              </div>
            </template>
          </div>
        </div>
      </template>

      <!-- Detail View -->
      <template x-if="!loading && !error && detail">
        <div class="proj-detail">
          <div class="proj-detail-header">
            <button class="proj-back-btn" @click="closeDetail()">\u2190 Projects</button>
            <h2 class="proj-detail-name" x-text="detail"></h2>
            <span class="proj-health-indicator" :style="'color:' + healthColor((cards[detail] || {}).health || 'healthy')">
              <span x-text="healthShape((cards[detail] || {}).health || 'healthy')"></span>
              <span class="proj-health-label" x-text="(cards[detail] || {}).healthLabel || 'Healthy'"></span>
            </span>
          </div>

          <!-- Detail stats -->
          <div class="proj-detail-stats">
            <div class="proj-detail-stat">
              <span class="proj-detail-stat-val" x-text="(cards[detail] || {}).pct + '%'"></span>
              <span class="proj-detail-stat-lbl">Complete</span>
            </div>
            <div class="proj-detail-stat">
              <span class="proj-detail-stat-val" x-text="(cards[detail] || {}).running || 0"></span>
              <span class="proj-detail-stat-lbl">Running</span>
            </div>
            <div class="proj-detail-stat">
              <span class="proj-detail-stat-val" x-text="(cards[detail] || {}).failed || 0"></span>
              <span class="proj-detail-stat-lbl">Failed</span>
            </div>
            <div class="proj-detail-stat">
              <span class="proj-detail-stat-val" x-text="(cards[detail] || {}).total || 0"></span>
              <span class="proj-detail-stat-lbl">Total</span>
            </div>
          </div>

          <!-- Task tree -->
          <template x-if="detailLoading">
            <div class="loading-state">loading tree\u2026</div>
          </template>

          <template x-if="!detailLoading">
            <div>
              <h3 class="proj-section-title">Task Tree</h3>
              <div class="proj-tree">
                <template x-for="item in tree" :key="item.id">
                  <div class="proj-tree-item"
                       x-show="isTreeVisible(item)"
                       :style="'padding-left:' + (item.depth * 20 + 8) + 'px'">
                    <template x-if="item.hasChildren">
                      <button class="proj-tree-toggle" @click="toggleCollapse(item.id)"
                              x-text="treeCollapsed[item.id] ? '\\u25B6' : '\\u25BC'"></button>
                    </template>
                    <template x-if="!item.hasChildren">
                      <span class="proj-tree-leaf">\u2022</span>
                    </template>
                    <span class="status-badge" :style="'background:' + statusColor(item.status)" x-text="item.status"></span>
                    <span class="proj-tree-title" x-text="truncate(item.title, 70)"></span>
                    <code class="proj-tree-id" x-text="item.id.slice(0, 8)"></code>
                  </div>
                </template>
                <template x-if="tree.length === 0">
                  <div class="steer-empty">No tasks in tree</div>
                </template>
              </div>

              <!-- Plans -->
              <template x-if="plans.length > 0">
                <div>
                  <h3 class="proj-section-title">Plans</h3>
                  <div class="proj-plans">
                    <template x-for="plan in plans" :key="plan.id">
                      <div class="proj-plan-row">
                        <span class="status-badge" :style="'background:' + statusColor(plan.status || 'open')" x-text="plan.status || 'draft'"></span>
                        <span class="proj-plan-title" x-text="plan.title || plan.id"></span>
                        <span class="proj-plan-date" x-text="timeAgo(plan.created_at)"></span>
                      </div>
                    </template>
                  </div>
                </div>
              </template>
            </div>
          </template>
        </div>
      </template>
    </div>`;
    Alpine.initTree(viewport);
  }

  function refresh(project) {
    const el = document.querySelector('[x-data="projectsPage"]');
    if (el && el._x_dataStack) el._x_dataStack[0].refresh();
  }

  App.registerView('projects', { render, refresh });
})();
