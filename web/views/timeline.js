/* ============================================================
   Timeline View — Gantt-style horizontal bar chart
   ============================================================ */

(() => {

  function render(viewport, project) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';

    App.API.timeline(project).then(data => {
      const tasks = (data.tasks || []).filter(t => t.created_at);
      if (tasks.length === 0) {
        viewport.innerHTML = '<div class="empty-state">No tasks with timestamps</div>';
        return;
      }
      viewport.innerHTML = '<div class="view-enter"><div class="timeline-container" id="timeline-root"></div></div>';
      drawTimeline(tasks);
    }).catch(err => {
      viewport.innerHTML = `<div class="empty-state">Failed to load timeline<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    });
  }

  function drawTimeline(tasks) {
    const container = document.getElementById('timeline-root');
    if (!container) return;

    const margin = { top: 32, right: 24, bottom: 32, left: 160 };
    const rowHeight = 26;
    const rowGap = 2;
    const width = Math.max(container.clientWidth, 600);
    const height = margin.top + margin.bottom + tasks.length * (rowHeight + rowGap);

    // Time extent
    const now = new Date();
    const parseDate = (s) => s ? new Date(s) : null;

    const timeData = tasks.map(t => {
      const start = parseDate(t.created_at) || now;
      let end = parseDate(t.updated_at);
      if (!end || end <= start) {
        // Use estimate or default 15min
        const estMs = (t.estimate_minutes || 15) * 60 * 1000;
        end = new Date(start.getTime() + estMs);
      }
      // For running tasks, extend to now
      if (t.status === 'running' && end < now) end = now;
      return { ...t, start, end };
    });

    const minDate = d3.min(timeData, d => d.start);
    const maxDate = d3.max(timeData, d => d.end);

    // Scales
    const xScale = d3.scaleTime()
      .domain([minDate, maxDate])
      .range([margin.left, width - margin.right]);

    const svg = d3.select(container)
      .append('svg')
      .attr('class', 'timeline-svg')
      .attr('width', width)
      .attr('height', height);

    // Grid lines
    const xAxis = d3.axisTop(xScale)
      .ticks(8)
      .tickSize(-(height - margin.top - margin.bottom))
      .tickFormat(d3.timeFormat('%b %d %H:%M'));

    svg.append('g')
      .attr('class', 'timeline-axis')
      .attr('transform', `translate(0,${margin.top})`)
      .call(xAxis)
      .call(g => g.select('.domain').remove())
      .call(g => g.selectAll('.tick line')
        .attr('class', 'timeline-grid')
        .attr('stroke-dasharray', '2,4'));

    // Bars
    const bars = svg.selectAll('.timeline-bar')
      .data(timeData)
      .join('g')
      .attr('class', 'timeline-bar')
      .attr('transform', (d, i) => `translate(0,${margin.top + i * (rowHeight + rowGap)})`);

    // Bar rect
    bars.append('rect')
      .attr('x', d => xScale(d.start))
      .attr('width', d => Math.max(xScale(d.end) - xScale(d.start), 4))
      .attr('height', rowHeight)
      .attr('fill', d => App.statusColor(d.status))
      .attr('opacity', 0.7);

    // Running tasks get a subtle pulse
    bars.filter(d => d.status === 'running')
      .select('rect')
      .attr('opacity', 0.85)
      .style('animation', 'pulse 2s ease-in-out infinite');

    // Task ID labels on the left
    bars.append('text')
      .attr('x', margin.left - 8)
      .attr('y', rowHeight / 2)
      .attr('text-anchor', 'end')
      .attr('dominant-baseline', 'central')
      .style('font-family', 'var(--font-mono)')
      .style('font-size', '10px')
      .style('fill', 'var(--text-tertiary)')
      .text(d => d.id);

    // Title labels inside bars (if they fit)
    bars.append('text')
      .attr('x', d => xScale(d.start) + 6)
      .attr('y', rowHeight / 2)
      .attr('dominant-baseline', 'central')
      .style('font-size', '10px')
      .style('fill', 'var(--text-primary)')
      .text(d => {
        const barWidth = xScale(d.end) - xScale(d.start);
        if (barWidth < 60) return '';
        const chars = Math.floor(barWidth / 6);
        return App.truncate(d.title, chars);
      });

    // Click handler
    bars.on('click', (event, d) => App.openPanel(d.id));

    // Tooltip on hover
    bars.append('title')
      .text(d => `${d.id}: ${d.title}\n${d.status} | ${App.formatDuration(d.actual_duration_sec)}`);
  }

  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (viewport) render(viewport, project);
  }

  App.registerView('timeline', { render, refresh });
})();
