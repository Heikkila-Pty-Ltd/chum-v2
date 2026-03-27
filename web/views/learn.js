/* ============================================================
   Learn — "Is the system improving?"
   Sparkline metrics, lesson feed, model performance table.
   Uses Alpine.js for declarative reactivity.
   ============================================================ */
(() => {
  document.addEventListener('alpine:init', () => {
    Alpine.data('learnPage', () => ({
      // State
      loading: true,
      error: null,
      trends: [],
      models: [],
      lessons: [],
      lessonSearch: '',
      projects: [],
      filterProject: '',

      // Lifecycle
      async init() {
        await this.load();
      },

      async load() {
        this.loading = true;
        this.error = null;
        try {
          const project = this.filterProject || App.currentProject;
          const [trendsRes, modelsRes, lessonsRes, projectsRes] = await Promise.allSettled([
            App.API.learningTrends(),
            App.API.modelPerf(),
            project ? App.API.lessons(project) : Promise.resolve({ lessons: [] }),
            App.API.projects(),
          ]);

          if (trendsRes.status === 'fulfilled') {
            this.trends = trendsRes.value.trends || [];
          }
          if (modelsRes.status === 'fulfilled') {
            this.models = modelsRes.value.models || [];
          }
          if (lessonsRes.status === 'fulfilled') {
            this.lessons = lessonsRes.value.lessons || [];
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
        const project = this.filterProject || App.currentProject;
        const [trendsRes, lessonsRes] = await Promise.allSettled([
          App.API.learningTrends(),
          project ? App.API.lessons(project) : Promise.resolve({ lessons: [] }),
        ]);
        if (trendsRes.status === 'fulfilled') this.trends = trendsRes.value.trends || [];
        if (lessonsRes.status === 'fulfilled') this.lessons = lessonsRes.value.lessons || [];
      },

      // --- Sparkline metrics ---

      get latestSuccessRate() {
        const recent = this.trends.filter(t => t.total_runs > 0);
        if (recent.length === 0) return null;
        return Math.round(recent[recent.length - 1].success_rate * 100);
      },

      get latestCostPerTask() {
        const recent = this.trends.filter(t => t.total_runs > 0);
        if (recent.length === 0) return null;
        const last = recent[recent.length - 1];
        return last.total_runs > 0 ? (last.total_cost_usd / last.total_runs) : 0;
      },

      get latestAvgDuration() {
        const recent = this.trends.filter(t => t.total_runs > 0);
        if (recent.length === 0) return null;
        return Math.round(recent[recent.length - 1].avg_duration_s);
      },

      trendDirection(values) {
        if (values.length < 2) return 'flat';
        const recent = values.slice(-7).filter(v => v !== null && v !== undefined);
        if (recent.length < 2) return 'flat';
        const first = recent[0];
        const last = recent[recent.length - 1];
        if (last > first * 1.05) return 'up';
        if (last < first * 0.95) return 'down';
        return 'flat';
      },

      trendPct(values) {
        if (values.length < 2) return 0;
        const recent = values.slice(-7).filter(v => v !== null && v !== undefined);
        if (recent.length < 2 || recent[0] === 0) return 0;
        return Math.round(((recent[recent.length - 1] - recent[0]) / recent[0]) * 100);
      },

      trendArrow(dir) {
        if (dir === 'up') return '\u2191';
        if (dir === 'down') return '\u2193';
        return '\u2192';
      },

      sparklineSVG(values, higherIsBetter) {
        if (!values || values.length === 0) return '';
        const W = 100, H = 30, pad = 2;
        const min = Math.min(...values);
        const max = Math.max(...values);
        const range = max - min || 1;
        const gradId = 'sg-' + Math.random().toString(36).slice(2, 8);

        const points = values.map((v, i) => {
          const x = pad + ((W - 2 * pad) * i) / Math.max(values.length - 1, 1);
          const y = pad + (H - 2 * pad) * (1 - (v - min) / range);
          return `${x.toFixed(1)},${y.toFixed(1)}`;
        });

        const dir = this.trendDirection(values);
        let color = '#6b7280'; // flat
        if (dir === 'up') color = higherIsBetter ? '#3d9a5f' : '#b93a3a';
        if (dir === 'down') color = higherIsBetter ? '#b93a3a' : '#3d9a5f';

        const polyPoints = points.join(' ');
        const areaPoints = `${pad},${H - pad} ${polyPoints} ${W - pad},${H - pad}`;

        return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" class="learn-sparkline" role="img" aria-label="Trend chart">
          <defs><linearGradient id="${gradId}" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="${color}" stop-opacity="0.3"/>
            <stop offset="100%" stop-color="${color}" stop-opacity="0.02"/>
          </linearGradient></defs>
          <polygon points="${areaPoints}" fill="url(#${gradId})"/>
          <polyline points="${polyPoints}" fill="none" stroke="${color}" stroke-width="1.5" stroke-linejoin="round"/>
        </svg>`;
      },

      get successRateValues() { return this.trends.map(t => t.success_rate * 100); },
      get costPerTaskValues() { return this.trends.map(t => t.total_runs > 0 ? t.total_cost_usd / t.total_runs : 0); },
      get durationValues() { return this.trends.map(t => t.avg_duration_s); },

      // --- Lessons ---

      get groupedLessons() {
        const groups = {};
        const filtered = this.filteredLessons;
        for (const l of filtered) {
          const key = l.category + '::' + l.summary;
          if (!groups[key]) {
            groups[key] = { ...l, count: 1 };
          } else {
            groups[key].count++;
            // Keep the most recent timestamp
            if (l.created_at > groups[key].created_at) {
              groups[key].created_at = l.created_at;
            }
          }
        }
        return Object.values(groups).sort((a, b) => {
          // Repeated lessons first (higher count), then by recency
          if (b.count !== a.count) return b.count - a.count;
          return (b.created_at || '') > (a.created_at || '') ? 1 : -1;
        });
      },

      get filteredLessons() {
        if (!this.lessonSearch) return this.lessons;
        const q = this.lessonSearch.toLowerCase();
        return this.lessons.filter(l =>
          (l.summary || '').toLowerCase().includes(q) ||
          (l.category || '').toLowerCase().includes(q)
        );
      },

      get patternCount() {
        return this.lessons.filter(l => l.category === 'pattern').length;
      },
      get antipatternCount() {
        return this.lessons.filter(l => l.category === 'antipattern').length;
      },

      categoryColor(cat) {
        switch (cat) {
          case 'pattern': return 'var(--status-completed)';
          case 'antipattern': return 'var(--status-failed)';
          case 'rule': return 'var(--accent)';
          case 'insight': return 'var(--status-dod-failed)';
          default: return 'var(--text-secondary)';
        }
      },

      // --- Model performance ---

      successRateClass(rate) {
        if (rate >= 0.8) return 'learn-rate--good';
        if (rate >= 0.6) return 'learn-rate--ok';
        return 'learn-rate--bad';
      },

      successRateLabel(rate) {
        if (rate >= 0.8) return 'Good';
        if (rate >= 0.6) return 'Fair';
        return 'Poor';
      },

      // --- Helpers ---
      timeAgo(ts) { return App.timeAgo(ts); },
      formatCost(c) { return c != null ? '$' + c.toFixed(3) : ''; },
      formatDuration(s) {
        if (s == null) return '';
        if (s < 60) return Math.round(s) + 's';
        return Math.round(s / 60) + 'm';
      },
      truncate(s, n) { return App.truncate(s, n || 100); },
    }));
  });

  function render(viewport, project) {
    viewport.innerHTML = `
    <div x-data="learnPage" class="learn-page view-enter">
      <!-- Loading -->
      <template x-if="loading">
        <div class="loading-state">loading\u2026</div>
      </template>

      <!-- Error -->
      <template x-if="error && !loading">
        <div class="empty-state">
          Failed to load learning data
          <div class="empty-state-hint" x-text="error"></div>
        </div>
      </template>

      <!-- Content -->
      <template x-if="!loading && !error">
        <div>
          <!-- Headline Metrics with Sparklines -->
          <div class="learn-metrics">
            <div class="learn-metric-card">
              <div class="learn-metric-header">
                <span class="learn-metric-value" x-text="latestSuccessRate !== null ? latestSuccessRate + '%' : '\\u2014'"></span>
                <span class="learn-metric-trend"
                      :class="'learn-trend--' + trendDirection(successRateValues)"
                      x-text="trendArrow(trendDirection(successRateValues)) + ' ' + Math.abs(trendPct(successRateValues)) + '%'"></span>
              </div>
              <div class="learn-metric-label">Success Rate (30d)</div>
              <div x-html="sparklineSVG(successRateValues, true)"></div>
            </div>

            <div class="learn-metric-card">
              <div class="learn-metric-header">
                <span class="learn-metric-value" x-text="latestCostPerTask !== null ? '$' + latestCostPerTask.toFixed(3) : '\\u2014'"></span>
                <span class="learn-metric-trend"
                      :class="'learn-trend--' + trendDirection(costPerTaskValues)"
                      x-text="trendArrow(trendDirection(costPerTaskValues)) + ' ' + Math.abs(trendPct(costPerTaskValues)) + '%'"></span>
              </div>
              <div class="learn-metric-label">Cost / Task (30d)</div>
              <div x-html="sparklineSVG(costPerTaskValues, false)"></div>
            </div>

            <div class="learn-metric-card">
              <div class="learn-metric-header">
                <span class="learn-metric-value" x-text="latestAvgDuration !== null ? formatDuration(latestAvgDuration) : '\\u2014'"></span>
                <span class="learn-metric-trend"
                      :class="'learn-trend--' + trendDirection(durationValues)"
                      x-text="trendArrow(trendDirection(durationValues)) + ' ' + Math.abs(trendPct(durationValues)) + '%'"></span>
              </div>
              <div class="learn-metric-label">Avg Duration (30d)</div>
              <div x-html="sparklineSVG(durationValues, false)"></div>
            </div>
          </div>

          <!-- Lesson Feed -->
          <section class="learn-section">
            <div class="learn-section-header">
              <h2 class="learn-section-title">Lessons</h2>
              <div class="learn-lesson-counts">
                <span class="learn-count-badge learn-count-badge--pattern" x-text="patternCount + ' patterns'"></span>
                <span class="learn-count-badge learn-count-badge--anti" x-text="antipatternCount + ' antipatterns'"></span>
              </div>
            </div>

            <div class="learn-filters">
              <input type="text" class="learn-search" placeholder="Search lessons\u2026"
                     x-model.debounce.300ms="lessonSearch">
              <select class="learn-filter-select" x-model="filterProject" @change="load()">
                <option value="">All Projects</option>
                <template x-for="p in projects" :key="p">
                  <option :value="p" x-text="p"></option>
                </template>
              </select>
            </div>

            <template x-if="groupedLessons.length === 0">
              <div class="steer-empty">No lessons found</div>
            </template>

            <div class="learn-lesson-list">
              <template x-for="lesson in groupedLessons" :key="lesson.id || (lesson.category + lesson.summary)">
                <div class="learn-lesson-row">
                  <span class="learn-lesson-cat" :style="'background:' + categoryColor(lesson.category)"
                        x-text="lesson.category"></span>
                  <template x-if="lesson.count > 1">
                    <span class="learn-lesson-count" x-text="'x' + lesson.count"></span>
                  </template>
                  <span class="learn-lesson-summary" x-text="truncate(lesson.summary, 120)"></span>
                  <span class="learn-lesson-time" x-text="timeAgo(lesson.created_at)"></span>
                </div>
              </template>
            </div>
          </section>

          <!-- Model Performance Table -->
          <section class="learn-section">
            <h2 class="learn-section-title">Model Performance</h2>

            <template x-if="models.length === 0">
              <div class="steer-empty">No performance data</div>
            </template>

            <template x-if="models.length > 0">
              <div class="learn-table-wrap">
                <table class="learn-table">
                  <thead>
                    <tr>
                      <th>Model</th>
                      <th>Tasks</th>
                      <th>Success</th>
                      <th>Avg Cost</th>
                      <th>Avg Duration</th>
                    </tr>
                  </thead>
                  <tbody>
                    <template x-for="m in models" :key="m.agent + m.model + m.tier">
                      <tr>
                        <td>
                          <span class="learn-model-name" x-text="m.model || m.agent"></span>
                          <span class="learn-model-tier" x-text="m.tier"></span>
                        </td>
                        <td class="learn-td-num" x-text="m.total_runs"></td>
                        <td>
                          <span class="learn-rate" :class="successRateClass(m.success_rate)">
                            <span x-text="Math.round(m.success_rate * 100) + '%'"></span>
                            <span class="learn-rate-label" x-text="successRateLabel(m.success_rate)"></span>
                          </span>
                        </td>
                        <td class="learn-td-num" x-text="formatCost(m.avg_cost_usd)"></td>
                        <td class="learn-td-num" x-text="formatDuration(m.avg_duration_s)"></td>
                      </tr>
                    </template>
                  </tbody>
                </table>
              </div>
            </template>
          </section>
        </div>
      </template>
    </div>`;
    Alpine.initTree(viewport);
  }

  function refresh(project) {
    const el = document.querySelector('[x-data="learnPage"]');
    if (el && el._x_dataStack) el._x_dataStack[0].refresh();
  }

  App.registerView('learn', { render, refresh });
})();
