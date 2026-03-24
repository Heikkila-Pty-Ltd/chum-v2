/* ============================================================
   Check — "What happened since I last looked?"
   Summary strip (KPI cards) + reverse-chronological activity feed.
   Uses Alpine.js for declarative reactivity.
   ============================================================ */
(() => {
  const ATTENTION_STATUSES = new Set([
    'failed', 'dod_failed', 'needs_refinement', 'needs_review',
    'rejected', 'quarantined', 'budget_exceeded',
  ]);
  const COMPLETED_STATUSES = new Set(['completed', 'done']);

  document.addEventListener('alpine:init', () => {
    Alpine.data('checkPage', () => ({
      // State
      loading: true,
      error: null,
      events: [],
      summary: { completed: 0, failed: 0, running: 0, total_cost: 0 },
      health: null,
      attention: [],
      filterProject: '',
      filterSeverity: 'all',
      projects: [],
      expanded: {},

      // Lifecycle
      async init() {
        await this.load();
      },

      async load() {
        this.loading = true;
        this.error = null;
        try {
          const [activityRes, healthRes, overviewRes, projectsRes] = await Promise.allSettled([
            App.API.activity(24, this.filterProject || undefined),
            App.API.health(),
            App.API.overview(App.currentProject),
            App.API.projects(),
          ]);

          if (activityRes.status === 'fulfilled') {
            this.events = activityRes.value.events || [];
            this.summary = activityRes.value.summary || this.summary;
          }
          if (healthRes.status === 'fulfilled') {
            this.health = healthRes.value;
          }
          if (overviewRes.status === 'fulfilled') {
            this.attention = overviewRes.value.attention || [];
          }
          if (projectsRes.status === 'fulfilled') {
            this.projects = projectsRes.value.projects || [];
          }
        } catch (err) {
          this.error = err.message;
        }
        this.loading = false;
      },

      async refresh() {
        // Re-fetch without resetting loading state (background refresh).
        try {
          const [activityRes, overviewRes] = await Promise.allSettled([
            App.API.activity(24, this.filterProject || undefined),
            App.API.overview(App.currentProject),
          ]);
          if (activityRes.status === 'fulfilled') {
            this.events = activityRes.value.events || [];
            this.summary = activityRes.value.summary || this.summary;
          }
          if (overviewRes.status === 'fulfilled') {
            this.attention = overviewRes.value.attention || [];
          }
        } catch { /* silent on background refresh */ }
      },

      // Computed
      get attentionCount() { return this.attention.length; },
      get successRate() {
        if (!this.health) return null;
        const counts = this.health.TaskStatusCounts || {};
        const total = Object.values(counts).reduce((a, b) => a + b, 0);
        const completed = (counts.completed || 0) + (counts.done || 0);
        return total > 0 ? Math.round((completed / total) * 100) : 0;
      },
      get quarantineCount() {
        return this.health ? (this.health.QuarantineCount || 0) : 0;
      },

      get filteredEvents() {
        return this.events.filter(ev => {
          if (this.filterProject && ev.project !== this.filterProject) return false;
          if (this.filterSeverity === 'attention') {
            return ev.type === 'task' && ATTENTION_STATUSES.has(ev.status);
          }
          if (this.filterSeverity === 'completed') {
            return ev.type === 'task' && COMPLETED_STATUSES.has(ev.status);
          }
          if (this.filterSeverity === 'lessons') {
            return ev.type === 'lesson';
          }
          return true;
        });
      },

      // Helpers
      timeAgo(ts) { return App.timeAgo(ts); },
      statusColor(s) { return App.statusColor(s); },
      formatCost(c) {
        if (!c || c === 0) return '';
        return '$' + c.toFixed(3);
      },
      truncate(s, n) { return App.truncate(s, n || 120); },
      escapeHtml(s) { return App.escapeHtml(s); },

      toggleExpand(idx) {
        this.expanded[idx] = !this.expanded[idx];
      },
      isExpanded(idx) {
        return !!this.expanded[idx];
      },

      isAttention(status) { return ATTENTION_STATUSES.has(status); },
    }));
  });

  function render(viewport, project) {
    viewport.innerHTML = `
    <div x-data="checkPage" class="check-page view-enter">
      <!-- Loading -->
      <template x-if="loading">
        <div class="loading-state">loading\u2026</div>
      </template>

      <!-- Error -->
      <template x-if="error && !loading">
        <div class="empty-state">
          Failed to load activity
          <div class="empty-state-hint" x-text="error"></div>
        </div>
      </template>

      <!-- Content -->
      <template x-if="!loading && !error">
        <div>
          <!-- Summary Strip -->
          <div class="check-strip">
            <a href="#/steer" class="check-card check-card--attention">
              <span class="check-card-value" x-text="attentionCount"></span>
              <span class="check-card-label">Needs Attention</span>
            </a>
            <div class="check-card">
              <span class="check-card-value" x-text="summary.completed"></span>
              <span class="check-card-label">Completed 24h</span>
            </div>
            <div class="check-card">
              <span class="check-card-value" x-text="summary.running"></span>
              <span class="check-card-label">In Progress</span>
            </div>
            <div class="check-card">
              <span class="check-card-value" x-text="formatCost(summary.total_cost) || '$0'"></span>
              <span class="check-card-label">Cost 24h</span>
            </div>
            <div class="check-card">
              <span class="check-card-value" x-text="successRate !== null ? successRate + '%' : '\u2014'"></span>
              <span class="check-card-label">Success Rate</span>
            </div>
            <template x-if="quarantineCount > 0">
              <div class="check-card check-card--warn">
                <span class="check-card-value" x-text="quarantineCount"></span>
                <span class="check-card-label">Quarantined</span>
              </div>
            </template>
          </div>

          <!-- Filter Bar -->
          <div class="check-filters">
            <select x-model="filterProject" @change="load()" class="check-filter-select">
              <option value="">All Projects</option>
              <template x-for="p in projects" :key="p">
                <option :value="p" x-text="p"></option>
              </template>
            </select>
            <div class="check-filter-pills">
              <button class="check-pill" :class="{ active: filterSeverity === 'all' }" @click="filterSeverity = 'all'">All</button>
              <button class="check-pill" :class="{ active: filterSeverity === 'attention' }" @click="filterSeverity = 'attention'">Attention</button>
              <button class="check-pill" :class="{ active: filterSeverity === 'completed' }" @click="filterSeverity = 'completed'">Completed</button>
              <button class="check-pill" :class="{ active: filterSeverity === 'lessons' }" @click="filterSeverity = 'lessons'">Lessons</button>
            </div>
          </div>

          <!-- Timeline Feed -->
          <div class="check-timeline">
            <template x-if="filteredEvents.length === 0">
              <div class="empty-state">No activity in the last 24 hours</div>
            </template>
            <template x-for="(ev, idx) in filteredEvents" :key="idx">
              <div class="check-event" :class="{ 'check-event--attention': isAttention(ev.status) }">
                <div class="check-event-time" x-text="timeAgo(ev.timestamp)"></div>
                <div class="check-event-body">
                  <div class="check-event-row">
                    <span class="check-event-type-badge" :class="'badge-' + ev.type" x-text="ev.type"></span>
                    <a class="task-link" :href="ev.task_id ? '#/work/' + ev.task_id : '#'" x-text="ev.task_id ? ev.task_id.slice(0, 12) : ''" @click.stop></a>
                    <span class="check-event-title" x-text="ev.title || ev.summary || ''"></span>
                    <span class="check-event-project" x-text="ev.project"></span>
                    <span class="status-badge" :style="'background:' + statusColor(ev.status)" x-text="ev.status"></span>
                    <template x-if="ev.cost_usd > 0">
                      <span class="check-event-cost" x-text="formatCost(ev.cost_usd)"></span>
                    </template>
                  </div>
                  <!-- Expandable detail for failed tasks and lessons -->
                  <template x-if="(isAttention(ev.status) && ev.outcome) || ev.type === 'lesson'">
                    <div>
                      <button class="check-expand-btn" @click="toggleExpand(idx)">
                        <span x-text="isExpanded(idx) ? '\u25bc' : '\u25b6'"></span>
                        <span x-text="ev.type === 'lesson' ? 'View lesson' : 'View error'"></span>
                      </button>
                      <div x-show="isExpanded(idx)" x-transition class="check-event-detail">
                        <template x-if="ev.type === 'lesson'">
                          <div>
                            <span class="check-lesson-category" x-text="ev.category"></span>
                            <span x-text="ev.summary"></span>
                          </div>
                        </template>
                        <template x-if="ev.type !== 'lesson'">
                          <pre class="check-error-log" x-text="ev.outcome"></pre>
                        </template>
                      </div>
                    </div>
                  </template>
                </div>
              </div>
            </template>
          </div>
        </div>
      </template>
    </div>`;
    Alpine.initTree(viewport);
  }

  function refresh(project) {
    const el = document.querySelector('[x-data="checkPage"]');
    if (el && el._x_dataStack) el._x_dataStack[0].refresh();
  }

  App.registerView('check', { render, refresh });
})();
