/* ============================================================
   DAG View — Force-directed + Dagre hierarchical layout toggle
   ============================================================ */

(() => {
  let simulation = null;
  let svg = null;
  let zoomBehavior = null;
  let cachedData = null;
  let layoutMode = 'dagre'; // 'dagre' (default) or 'force'

  function render(viewport, project) {
    viewport.innerHTML = `
      <div class="dag-container view-enter" id="dag-root">
        <div class="dag-controls">
          <button class="dag-control-btn ${layoutMode === 'force' ? 'active' : ''}" id="dag-layout-force" title="Force layout (F)">F</button>
          <button class="dag-control-btn ${layoutMode === 'dagre' ? 'active' : ''}" id="dag-layout-dagre" title="Hierarchical layout (H)">H</button>
          <button class="dag-control-btn" id="dag-zoom-in" title="Zoom in">+</button>
          <button class="dag-control-btn" id="dag-zoom-reset" title="Reset zoom">\u25a3</button>
          <button class="dag-control-btn" id="dag-zoom-out" title="Zoom out">\u2212</button>
        </div>
      </div>
    `;

    // Tooltip
    let tooltip = document.querySelector('.dag-tooltip');
    if (!tooltip) {
      tooltip = document.createElement('div');
      tooltip.className = 'dag-tooltip';
      document.body.appendChild(tooltip);
    }

    App.API.graph(project).then(data => {
      cachedData = data;
      drawGraph(data, tooltip);
    }).catch(err => {
      viewport.innerHTML = `<div class="empty-state">Failed to load graph<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    });
  }

  function drawGraph(data, tooltip) {
    const container = document.getElementById('dag-root');
    if (!container) return;

    const width = container.clientWidth;
    const height = container.clientHeight;

    // Clear previous
    if (simulation) { simulation.stop(); simulation = null; }
    container.querySelectorAll('svg').forEach(s => s.remove());

    svg = d3.select(container)
      .insert('svg', ':first-child')
      .attr('width', width)
      .attr('height', height);

    // Defs: arrowhead marker
    const defs = svg.append('defs');
    defs.append('marker')
      .attr('id', 'arrowhead')
      .attr('viewBox', '0 -4 8 8')
      .attr('refX', 20)
      .attr('refY', 0)
      .attr('markerWidth', 6)
      .attr('markerHeight', 6)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,-3L7,0L0,3')
      .attr('fill', '#3a3e48');

    const g = svg.append('g');

    // Zoom
    zoomBehavior = d3.zoom()
      .scaleExtent([0.15, 4])
      .on('zoom', (event) => g.attr('transform', event.transform));
    svg.call(zoomBehavior);

    // Zoom controls
    document.getElementById('dag-zoom-in').onclick = () =>
      svg.transition().duration(300).call(zoomBehavior.scaleBy, 1.4);
    document.getElementById('dag-zoom-out').onclick = () =>
      svg.transition().duration(300).call(zoomBehavior.scaleBy, 0.7);
    document.getElementById('dag-zoom-reset').onclick = () =>
      svg.transition().duration(300).call(zoomBehavior.transform, d3.zoomIdentity);

    // Layout toggle
    document.getElementById('dag-layout-force').onclick = () => {
      layoutMode = 'force';
      updateLayoutButtons();
      drawGraph(cachedData, tooltip);
    };
    document.getElementById('dag-layout-dagre').onclick = () => {
      layoutMode = 'dagre';
      updateLayoutButtons();
      drawGraph(cachedData, tooltip);
    };

    // Node dimensions
    const nodeWidth = 140;
    const nodeHeight = 36;

    // Prepare node map
    const nodeMap = new Map();
    const nodes = data.nodes.map(n => {
      const node = { ...n };
      nodeMap.set(n.id, node);
      return node;
    });

    // Filter edges to valid ones
    const links = data.edges
      .filter(e => nodeMap.has(e.from) && nodeMap.has(e.to))
      .map(e => ({ source: e.from, target: e.to }));

    if (layoutMode === 'dagre') {
      drawDagre(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip);
    } else {
      drawForce(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip);
    }

    // Click background to deselect
    svg.on('click', () => App.closePanel());
  }

  function drawDagre(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip) {
    // Create dagre graph
    const dg = new dagre.graphlib.Graph();
    dg.setGraph({ rankdir: 'TB', nodesep: 30, ranksep: 80 });
    dg.setDefaultEdgeLabel(() => ({}));

    nodes.forEach(n => {
      dg.setNode(n.id, { width: nodeWidth, height: nodeHeight });
    });

    links.forEach(l => {
      dg.setEdge(l.source, l.target);
    });

    dagre.layout(dg);

    // Read positions back
    nodes.forEach(n => {
      const pos = dg.node(n.id);
      n.x = pos.x;
      n.y = pos.y;
    });

    // Draw edges as curved paths
    const line = d3.line().curve(d3.curveBasis);

    g.append('g')
      .selectAll('path')
      .data(links)
      .join('path')
      .attr('class', 'dag-edge')
      .attr('d', l => {
        const edge = dg.edge(l.source, l.target);
        const points = edge.points || [];
        if (points.length === 0) {
          const src = nodeMap.get(l.source);
          const tgt = nodeMap.get(l.target);
          return line([[src.x, src.y], [tgt.x, tgt.y]]);
        }
        return line(points.map(p => [p.x, p.y]));
      })
      .attr('marker-end', 'url(#arrowhead)');

    // Draw nodes
    const node = g.append('g')
      .selectAll('g')
      .data(nodes)
      .join('g')
      .attr('class', 'dag-node')
      .attr('transform', d => `translate(${d.x},${d.y})`);

    appendNodeElements(node, nodeWidth, nodeHeight, tooltip);

    // Fit to bounds
    fitBounds(nodes, width, height);
  }

  function drawForce(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip) {
    // Initialize positions
    nodes.forEach(n => {
      n.x = width / 2 + (Math.random() - 0.5) * 400;
      n.y = height / 2 + (Math.random() - 0.5) * 400;
    });

    // Edges as lines
    const link = g.append('g')
      .selectAll('line')
      .data(links)
      .join('line')
      .attr('class', 'dag-edge');

    // Node groups
    const node = g.append('g')
      .selectAll('g')
      .data(nodes)
      .join('g')
      .attr('class', 'dag-node')
      .call(d3.drag()
        .on('start', dragStarted)
        .on('drag', dragged)
        .on('end', dragEnded));

    appendNodeElements(node, nodeWidth, nodeHeight, tooltip);

    // Simulation
    simulation = d3.forceSimulation(nodes)
      .force('link', d3.forceLink(links).id(d => d.id).distance(120).strength(0.4))
      .force('charge', d3.forceManyBody().strength(-300))
      .force('center', d3.forceCenter(width / 2, height / 2))
      .force('collision', d3.forceCollide().radius(nodeWidth / 2 + 8))
      .force('y', d3.forceY(height / 2).strength(0.03))
      .on('tick', () => {
        link
          .attr('x1', d => d.source.x)
          .attr('y1', d => d.source.y)
          .attr('x2', d => d.target.x)
          .attr('y2', d => d.target.y);
        node.attr('transform', d => `translate(${d.x},${d.y})`);
      });

    simulation.on('end', () => fitBounds(nodes, width, height));
    setTimeout(() => fitBounds(nodes, width, height), 1500);
  }

  function appendNodeElements(node, nodeWidth, nodeHeight, tooltip) {
    // Node rect
    node.append('rect')
      .attr('width', nodeWidth)
      .attr('height', nodeHeight)
      .attr('x', -nodeWidth / 2)
      .attr('y', -nodeHeight / 2)
      .attr('fill', d => {
        const c = App.statusColor(d.status);
        return d3.color(c).darker(2.5).toString();
      })
      .attr('stroke', d => App.statusColor(d.status))
      .attr('stroke-opacity', 0.6);

    // Status bar on left
    node.append('rect')
      .attr('width', 3)
      .attr('height', nodeHeight)
      .attr('x', -nodeWidth / 2)
      .attr('y', -nodeHeight / 2)
      .attr('fill', d => App.statusColor(d.status))
      .attr('rx', 0);

    // Title text
    node.append('text')
      .attr('x', -nodeWidth / 2 + 10)
      .attr('y', -2)
      .text(d => App.truncate(d.title, 18));

    // ID text
    node.append('text')
      .attr('class', 'node-id')
      .attr('x', -nodeWidth / 2 + 10)
      .attr('y', 11)
      .text(d => d.id);

    // Interactions
    node.on('click', (event, d) => {
      event.stopPropagation();
      App.openPanel(d.id);
    });

    node.on('mouseenter', (event, d) => {
      tooltip.innerHTML = `
        <div class="dag-tooltip-id">${App.escapeHtml(d.id)}</div>
        <div>${App.escapeHtml(d.title)}</div>
        <div style="margin-top:4px">${App.statusBadge(d.status)}</div>
      `;
      tooltip.classList.add('visible');
    });

    node.on('mousemove', (event) => {
      tooltip.style.left = (event.clientX + 12) + 'px';
      tooltip.style.top = (event.clientY - 10) + 'px';
    });

    node.on('mouseleave', () => {
      tooltip.classList.remove('visible');
    });
  }

  function updateLayoutButtons() {
    const forceBtn = document.getElementById('dag-layout-force');
    const dagreBtn = document.getElementById('dag-layout-dagre');
    if (forceBtn) forceBtn.classList.toggle('active', layoutMode === 'force');
    if (dagreBtn) dagreBtn.classList.toggle('active', layoutMode === 'dagre');
  }

  function fitBounds(nodes, width, height) {
    if (!nodes.length || !svg || !zoomBehavior) return;
    const xs = nodes.map(n => n.x);
    const ys = nodes.map(n => n.y);
    const x0 = Math.min(...xs) - 100;
    const x1 = Math.max(...xs) + 100;
    const y0 = Math.min(...ys) - 60;
    const y1 = Math.max(...ys) + 60;

    const bw = x1 - x0;
    const bh = y1 - y0;
    const scale = Math.min(width / bw, height / bh, 1.5) * 0.9;
    const tx = width / 2 - (x0 + bw / 2) * scale;
    const ty = height / 2 - (y0 + bh / 2) * scale;

    svg.transition().duration(500).call(
      zoomBehavior.transform,
      d3.zoomIdentity.translate(tx, ty).scale(scale)
    );
  }

  function dragStarted(event, d) {
    if (!event.active) simulation.alphaTarget(0.1).restart();
    d.fx = d.x;
    d.fy = d.y;
  }

  function dragged(event, d) {
    d.fx = event.x;
    d.fy = event.y;
  }

  function dragEnded(event, d) {
    if (!event.active) simulation.alphaTarget(0);
    d.fx = null;
    d.fy = null;
  }

  function graphFingerprint(data) {
    const nodes = data.nodes.map(n => n.id + ':' + n.status + ':' + n.title).sort().join('|');
    const edges = data.edges.map(e => e.from + '>' + e.to).sort().join('|');
    return nodes + '||' + edges;
  }

  function refresh(project) {
    App.API.graph(project).then(data => {
      if (!cachedData || graphFingerprint(data) !== graphFingerprint(cachedData)) {
        cachedData = data;
        const tooltip = document.querySelector('.dag-tooltip');
        drawGraph(data, tooltip);
      }
    }).catch(() => {});
  }

  App.registerView('dag', { render, refresh });
})();
