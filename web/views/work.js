/* ============================================================
   Work — "What are we building and why?"
   Master-detail: project picker + epic/task/subtask tree (left)
   with full task detail panel (right).
   Uses Alpine.js for declarative reactivity.
   ============================================================ */
(() => {
  const TERMINAL = new Set(['completed', 'done']);
  const FAILED = new Set([
    'failed', 'dod_failed', 'rejected', 'quarantined', 'budget_exceeded',
  ]);

  document.addEventListener('alpine:init', () => {
    Alpine.data('workPage', () => ({
      // --- State ---
      loading: true,
      error: null,
      projects: [],
      selectedProject: '',
      tree: [],               // flat tree items: { ...node, depth, hasChildren, childCount, completedCount }
      treeCollapsed: {},       // id -> true
      selectedTaskId: null,
      taskDetail: null,        // full API response from /api/dashboard/task/{id}
      taskTraces: [],
      detailLoading: false,
      detailError: null,
      filterText: '',
      filterStatus: '',
      _filterDebounce: null,

      // --- Lifecycle ---
      async init() {
        await this.load();
      },

      async load() {
        this.loading = true;
        this.error = null;
        try {
          const data = await App.API.projects();
          this.projects = data.projects || [];

          // If a deep-linked task was set before load, resolve its project.
          if (this._pendingTaskId) {
            await this.resolveDeepLink(this._pendingTaskId);
            this._pendingTaskId = null;
          } else {
            // Default to current project or first available.
            const proj = App.currentProject || this.projects[0] || '';
            if (proj) {
              this.selectedProject = proj;
              await this.loadTree();
            }
          }
        } catch (err) {
          this.error = err.message;
        }
        this.loading = false;
      },

      async loadTree() {
        if (!this.selectedProject) return;
        try {
          const data = await App.API.tree(this.selectedProject);
          this.tree = this.buildFlatTree(data.nodes || []);
        } catch (err) {
          this.tree = [];
          this.error = err.message;
        }
      },

      async switchProject() {
        // Full reset on project change.
        this.treeCollapsed = {};
        this.selectedTaskId = null;
        this.taskDetail = null;
        this.taskTraces = [];
        this.detailError = null;
        this.filterText = '';
        this.filterStatus = '';
        await this.loadTree();
      },

      // --- Deep link resolution ---
      async resolveDeepLink(taskId) {
        this.detailLoading = true;
        try {
          const data = await App.API.task(taskId);
          const proj = data.task.project;
          if (proj && this.projects.includes(proj)) {
            this.selectedProject = proj;
          } else if (proj) {
            this.selectedProject = proj;
          }
          await this.loadTree();

          // Auto-expand ancestors.
          this.expandAncestors(taskId);

          // Select the task and load detail.
          this.selectedTaskId = taskId;
          this.taskDetail = data;

          // Fetch traces in background (optional).
          App.API.traces(taskId).then(d => {
            this.taskTraces = d.traces || [];
          }).catch(() => { this.taskTraces = []; });

          // Scroll into view after render.
          this.$nextTick(() => {
            const el = document.querySelector(`[data-tree-id="${CSS.escape(taskId)}"]`);
            if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
          });
        } catch (err) {
          this.detailError = 'Task not found: ' + err.message;
        }
        this.detailLoading = false;
      },

      expandAncestors(taskId) {
        const byId = {};
        for (const n of this.tree) byId[n.id] = n;
        let node = byId[taskId];
        while (node && node.parent_id) {
          delete this.treeCollapsed[node.parent_id];
          node = byId[node.parent_id];
        }
      },

      // --- Task selection ---
      async selectTask(id) {
        if (this.selectedTaskId === id) return;
        this.selectedTaskId = id;
        this.detailLoading = true;
        this.detailError = null;
        this.taskTraces = [];
        try {
          const [taskRes, tracesRes] = await Promise.allSettled([
            App.API.task(id),
            App.API.traces(id),
          ]);
          if (taskRes.status === 'fulfilled') {
            this.taskDetail = taskRes.value;
          } else {
            throw taskRes.reason;
          }
          if (tracesRes.status === 'fulfilled') {
            this.taskTraces = tracesRes.value.traces || [];
          }
        } catch (err) {
          this.detailError = err.message;
          this.taskDetail = null;
        }
        this.detailLoading = false;
      },

      navigateToTask(id) {
        // Used for clicking deps/children links within detail panel.
        // Check if task is in current tree.
        const inTree = this.tree.some(n => n.id === id);
        if (inTree) {
          this.expandAncestors(id);
          this.selectTask(id);
          this.$nextTick(() => {
            const el = document.querySelector(`[data-tree-id="${CSS.escape(id)}"]`);
            if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
          });
        } else {
          // Different project — use hash navigation.
          location.hash = '#/work/' + id;
        }
      },

      // --- Tree building ---
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
          const children = childMap[id] || [];
          const hasChildren = children.length > 0;

          // Progress: count direct children statuses.
          let childCount = 0;
          let completedCount = 0;
          if (hasChildren) {
            for (const cid of children) {
              const child = byId[cid];
              if (child) {
                childCount++;
                if (TERMINAL.has(child.status)) completedCount++;
              }
            }
          }

          result.push({
            ...node,
            depth,
            hasChildren,
            childCount,
            completedCount,
          });

          for (const cid of children) {
            walk(cid, depth + 1);
          }
        };

        // Roots: no parent or parent not in set.
        for (const n of nodes) {
          if (!n.parent_id || !byId[n.parent_id]) {
            walk(n.id, 0);
          }
        }
        // Orphans.
        for (const n of nodes) {
          if (!visited.has(n.id)) {
            result.push({ ...n, depth: 0, hasChildren: false, childCount: 0, completedCount: 0 });
          }
        }
        return result;
      },

      // --- Tree visibility ---
      isTreeVisible(item) {
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
        if (this.treeCollapsed[id]) {
          delete this.treeCollapsed[id];
        } else {
          this.treeCollapsed[id] = true;
        }
      },

      // --- Filtering ---
      get filteredTree() {
        const text = this.filterText.toLowerCase().trim();
        const status = this.filterStatus;
        if (!text && !status) return this.tree;

        // Find matching node IDs.
        const matchIds = new Set();
        for (const node of this.tree) {
          const textMatch = !text || node.title.toLowerCase().includes(text) || node.id.toLowerCase().includes(text);
          const statusMatch = !status || node.status === status;
          if (textMatch && statusMatch) matchIds.add(node.id);
        }

        // Add ancestors of matching nodes to preserve hierarchy.
        const byId = {};
        for (const n of this.tree) byId[n.id] = n;
        const visible = new Set(matchIds);
        for (const id of matchIds) {
          let node = byId[id];
          while (node && node.parent_id) {
            visible.add(node.parent_id);
            node = byId[node.parent_id];
          }
        }

        return this.tree.filter(n => visible.has(n.id));
      },

      onFilterInput() {
        // Debounce text filter.
        clearTimeout(this._filterDebounce);
        this._filterDebounce = setTimeout(() => {
          // Force Alpine re-evaluation (filterText is already reactive).
        }, 200);
      },

      clearFilter() {
        this.filterText = '';
        this.filterStatus = '';
      },

      // --- Progress ---
      progressPct(node) {
        if (!node.hasChildren || node.childCount === 0) return 0;
        return Math.round((node.completedCount / node.childCount) * 100);
      },

      // --- Refresh ---
      async refresh() {
        // Preserve all user state; only update tree data + selected task.
        if (!this.selectedProject) return;
        try {
          const data = await App.API.tree(this.selectedProject);
          const newTree = this.buildFlatTree(data.nodes || []);
          // Merge: update existing nodes, add new, remove deleted.
          this.tree = newTree;
        } catch { /* silent on background refresh */ }

        // Refresh selected task detail if one is selected.
        if (this.selectedTaskId && this.taskDetail) {
          try {
            const taskRes = await App.API.task(this.selectedTaskId);
            this.taskDetail = taskRes;
          } catch { /* silent */ }
        }
      },

      // --- Detail rendering helpers ---
      get t() { return this.taskDetail ? this.taskDetail.task : null; },
      get deps() { return this.taskDetail ? (this.taskDetail.dependencies || []) : []; },
      get dependents() { return this.taskDetail ? (this.taskDetail.dependents || []) : []; },
      get targets() { return this.taskDetail ? (this.taskDetail.targets || []) : []; },
      get decisions() { return this.taskDetail ? (this.taskDetail.decisions || []) : []; },

      parentNode() {
        if (!this.t || !this.t.parent_id) return null;
        return this.tree.find(n => n.id === this.t.parent_id) || null;
      },
      childNodes() {
        if (!this.t) return [];
        return this.tree.filter(n => n.parent_id === this.t.id);
      },

      isFailed(status) { return FAILED.has(status); },

      shortPath(fp) {
        if (!fp) return '';
        return fp.split('/').slice(-2).join('/');
      },

      // --- Shared helpers ---
      timeAgo(ts) { return App.timeAgo(ts); },
      statusColor(s) { return App.statusColor(s); },
      truncate(s, n) { return App.truncate(s, n || 80); },
      escapeHtml(s) { return App.escapeHtml(s); },
      formatDuration(s) { return App.formatDuration(s); },
      formatMinutes(m) { return App.formatMinutes(m); },
    }));
  });

  function render(viewport, project, param) {
    viewport.innerHTML = `
    <div x-data="workPage" class="work-page view-enter" x-init="
      ${param ? `_pendingTaskId = '${param.replace(/'/g, "\\'")}';` : ''}
      init();
    ">
      <!-- Loading -->
      <template x-if="loading">
        <div class="loading-state">loading\u2026</div>
      </template>

      <!-- Error (no projects) -->
      <template x-if="error && !loading && projects.length === 0">
        <div class="empty-state">
          Failed to load
          <div class="empty-state-hint" x-text="error"></div>
        </div>
      </template>

      <!-- Main layout -->
      <template x-if="!loading">
        <div class="work-layout">
          <!-- LEFT PANEL: project picker + filter + tree -->
          <div class="work-left">
            <!-- Project picker -->
            <div class="work-project-picker">
              <select x-model="selectedProject" @change="switchProject()" class="work-project-select">
                <template x-for="p in projects" :key="p">
                  <option :value="p" x-text="p"></option>
                </template>
              </select>
            </div>

            <!-- Filter bar -->
            <div class="work-filter-bar">
              <input type="text" class="work-filter-input" placeholder="Filter tasks\u2026"
                     x-model="filterText" @input="onFilterInput()" @keydown.stop>
              <select class="work-filter-status" x-model="filterStatus" @keydown.stop>
                <option value="">All statuses</option>
                <option value="running">running</option>
                <option value="ready">ready</option>
                <option value="failed">failed</option>
                <option value="completed">completed</option>
                <option value="paused">paused</option>
                <option value="needs_review">needs review</option>
                <option value="dod_failed">dod failed</option>
                <option value="quarantined">quarantined</option>
              </select>
              <button class="work-filter-clear" x-show="filterText || filterStatus" @click="clearFilter()">\u00d7</button>
            </div>

            <!-- Tree -->
            <div class="work-tree">
              <template x-if="filteredTree.length === 0 && !loading">
                <div class="work-tree-empty" x-text="filterText || filterStatus ? 'No matching tasks' : 'No tasks in this project'"></div>
              </template>
              <template x-for="item in filteredTree" :key="item.id">
                <div class="work-tree-item"
                     x-show="isTreeVisible(item)"
                     :class="{ 'work-tree-selected': item.id === selectedTaskId }"
                     :style="'padding-left:' + (item.depth * 18 + 8) + 'px'"
                     :data-tree-id="item.id"
                     @click="selectTask(item.id)">
                  <!-- Collapse toggle -->
                  <template x-if="item.hasChildren">
                    <button class="work-tree-toggle" @click.stop="toggleCollapse(item.id)"
                            x-text="treeCollapsed[item.id] ? '\\u25B6' : '\\u25BC'"></button>
                  </template>
                  <template x-if="!item.hasChildren">
                    <span class="work-tree-leaf"></span>
                  </template>

                  <!-- Status dot -->
                  <span class="work-tree-dot" :style="'background:' + statusColor(item.status)"></span>

                  <!-- Title -->
                  <span class="work-tree-title" x-text="truncate(item.title, 50)"></span>

                  <!-- Progress bar for parent nodes -->
                  <template x-if="item.hasChildren && item.childCount > 0">
                    <span class="work-tree-progress">
                      <span class="work-tree-progress-bar">
                        <span class="work-tree-progress-fill" :style="'width:' + progressPct(item) + '%'"></span>
                      </span>
                      <span class="work-tree-progress-text" x-text="item.completedCount + '/' + item.childCount"></span>
                    </span>
                  </template>
                </div>
              </template>
            </div>
          </div>

          <!-- RIGHT PANEL: task detail -->
          <div class="work-right">
            <!-- No selection -->
            <template x-if="!selectedTaskId && !detailLoading">
              <div class="work-detail-empty">Select a task to view details</div>
            </template>

            <!-- Loading detail -->
            <template x-if="detailLoading">
              <div class="work-detail-loading">loading\u2026</div>
            </template>

            <!-- Error -->
            <template x-if="detailError && !detailLoading">
              <div class="work-detail-error" x-text="detailError"></div>
            </template>

            <!-- Task detail -->
            <template x-if="taskDetail && !detailLoading && !detailError">
              <div class="work-detail">
                <!-- Header -->
                <div class="work-detail-header">
                  <span class="work-detail-status" :style="'background:' + statusColor(t.status)" x-text="t.status"></span>
                  <code class="work-detail-id" x-text="t.id"></code>
                </div>
                <h2 class="work-detail-title" x-text="t.title"></h2>

                <!-- === PRIMARY: Narrative === -->
                <template x-if="t.description">
                  <div class="work-section">
                    <div class="work-section-label">Description</div>
                    <div class="work-section-body work-prose" x-text="t.description"></div>
                  </div>
                </template>

                <template x-if="t.acceptance">
                  <div class="work-section">
                    <div class="work-section-label">Acceptance Criteria</div>
                    <div class="work-section-body work-prose" x-text="t.acceptance"></div>
                  </div>
                </template>

                <!-- === PRIMARY: Decisions === -->
                <template x-if="decisions.length > 0">
                  <div class="work-section">
                    <div class="work-section-label">Decisions</div>
                    <template x-for="dec in decisions" :key="dec.id">
                      <div class="work-decision">
                        <div class="work-decision-title" x-text="dec.title"></div>
                        <div class="work-decision-outcome" x-text="dec.outcome"></div>
                        <template x-for="alt in (dec.alternatives || [])" :key="alt.id">
                          <div class="work-decision-alt" :class="{ 'work-decision-alt--selected': alt.selected }">
                            <span x-text="alt.selected ? '\\u25C9' : '\\u25CB'"></span>
                            <span x-text="alt.label"></span>
                            <template x-if="alt.uct_score > 0">
                              <span class="work-decision-uct" x-text="'uct:' + alt.uct_score.toFixed(2)"></span>
                            </template>
                            <template x-if="alt.visits > 0">
                              <span class="work-decision-visits" x-text="alt.visits + ' visits'"></span>
                            </template>
                          </div>
                        </template>
                      </div>
                    </template>
                  </div>
                </template>

                <!-- === PRIMARY: Relationships === -->
                <div class="work-section">
                  <div class="work-section-label">Relationships</div>
                  <div class="work-relationships">
                    <!-- Parent -->
                    <template x-if="parentNode()">
                      <div class="work-rel-row">
                        <span class="work-rel-label">Parent</span>
                        <a class="work-rel-link" href="#" @click.prevent="navigateToTask(parentNode().id)"
                           x-text="truncate(parentNode().title, 40)"></a>
                      </div>
                    </template>

                    <!-- Children -->
                    <template x-if="childNodes().length > 0">
                      <div class="work-rel-row">
                        <span class="work-rel-label">Children</span>
                        <div class="work-rel-list">
                          <template x-for="child in childNodes()" :key="child.id">
                            <a class="work-rel-link" href="#" @click.prevent="navigateToTask(child.id)">
                              <span class="work-tree-dot" :style="'background:' + statusColor(child.status)" style="display:inline-block;width:6px;height:6px;margin-right:4px"></span>
                              <span x-text="truncate(child.title, 30)"></span>
                            </a>
                          </template>
                        </div>
                      </div>
                    </template>

                    <!-- Dependencies -->
                    <template x-if="deps.length > 0">
                      <div class="work-rel-row">
                        <span class="work-rel-label">\u2190 Deps</span>
                        <div class="work-rel-list">
                          <template x-for="d in deps" :key="d">
                            <a class="work-rel-link" href="#" @click.prevent="navigateToTask(d)" x-text="d.slice(0, 12)"></a>
                          </template>
                        </div>
                      </div>
                    </template>

                    <!-- Dependents -->
                    <template x-if="dependents.length > 0">
                      <div class="work-rel-row">
                        <span class="work-rel-label">\u2192 Dependents</span>
                        <div class="work-rel-list">
                          <template x-for="d in dependents" :key="d">
                            <a class="work-rel-link" href="#" @click.prevent="navigateToTask(d)" x-text="d.slice(0, 12)"></a>
                          </template>
                        </div>
                      </div>
                    </template>

                    <!-- Code targets -->
                    <template x-if="targets.length > 0">
                      <div class="work-rel-row">
                        <span class="work-rel-label">Targets</span>
                        <div class="work-rel-list">
                          <template x-for="tgt in targets" :key="tgt.file_path + tgt.symbol_name">
                            <code class="work-target" x-text="shortPath(tgt.file_path) + (tgt.symbol_name ? ':' + tgt.symbol_name : '')"></code>
                          </template>
                        </div>
                      </div>
                    </template>

                    <!-- No relationships -->
                    <template x-if="!parentNode() && childNodes().length === 0 && deps.length === 0 && dependents.length === 0 && targets.length === 0">
                      <div class="work-rel-none">No relationships</div>
                    </template>
                  </div>
                </div>

                <!-- === SECONDARY: Execution (collapsible) === -->
                <details class="work-details-section">
                  <summary class="work-section-label work-section-toggle">Execution</summary>
                  <div class="work-execution">
                    <div class="work-stat-chips">
                      <template x-if="t.priority"><span class="work-stat-chip">P<span x-text="t.priority"></span></span></template>
                      <template x-if="t.attempt_count"><span class="work-stat-chip"><span x-text="t.attempt_count"></span> attempts</span></template>
                      <template x-if="t.estimate_minutes"><span class="work-stat-chip">est <span x-text="formatMinutes(t.estimate_minutes)"></span></span></template>
                      <template x-if="t.actual_duration_sec"><span class="work-stat-chip">took <span x-text="formatDuration(t.actual_duration_sec)"></span></span></template>
                      <template x-if="t.iterations_used"><span class="work-stat-chip"><span x-text="t.iterations_used"></span> iters</span></template>
                      <span class="work-stat-chip" x-text="timeAgo(t.created_at)"></span>
                    </div>
                  </div>
                </details>

                <!-- === SECONDARY: Error Log (collapsible, auto-open on failure) === -->
                <template x-if="t.error_log">
                  <details class="work-details-section" :open="isFailed(t.status)">
                    <summary class="work-section-label work-section-toggle">Error Log</summary>
                    <pre class="work-error-log" x-text="t.error_log"></pre>
                  </details>
                </template>

                <!-- === SECONDARY: Traces (collapsible) === -->
                <template x-if="taskTraces.length > 0">
                  <details class="work-details-section">
                    <summary class="work-section-label work-section-toggle">Traces (<span x-text="taskTraces.length"></span>)</summary>
                    <div class="work-traces">
                      <template x-for="(tr, i) in taskTraces" :key="i">
                        <div class="work-trace">
                          <span class="work-trace-num" x-text="'#' + (i + 1)"></span>
                          <span class="work-trace-outcome" :style="'color:' + (tr.success ? 'var(--status-completed)' : 'var(--status-failed)')" x-text="tr.outcome || tr.status || '?'"></span>
                          <template x-if="tr.duration_sec"><span class="work-trace-dur" x-text="formatDuration(tr.duration_sec)"></span></template>
                          <template x-if="tr.cost_usd"><span class="work-trace-cost" x-text="'$' + Number(tr.cost_usd).toFixed(4)"></span></template>
                        </div>
                      </template>
                    </div>
                  </details>
                </template>
              </div>
            </template>
          </div>
        </div>
      </template>
    </div>`;
    Alpine.initTree(viewport);
  }

  function refresh(project) {
    const el = document.querySelector('[x-data="workPage"]');
    if (el && el._x_dataStack) el._x_dataStack[0].refresh();
  }

  App.registerView('work', { render, refresh });
})();
