// infrachart canvas.
//
// The browser owns the chart while it is being edited. Markup for nodes always
// comes from the server, so this file never builds node HTML — it positions
// nodes, drags them, draws the connection lines, and keeps `state` in step.
(function() {
  'use strict'

  const canvas = document.getElementById('canvas')
  const canvasWrap = document.getElementById('canvas-wrap')
  let linesLayer = document.getElementById('lines-layer')

  let state = readSessionData()
  let catalog = {}
  let connectFrom = null
  let selection = null
  // CSS `zoom` rather than a transform, because it affects layout — so the
  // scroll area shrinks with the chart instead of leaving the scrollbars
  // sized for a canvas that is no longer that big.
  let zoom = 1
  const MIN_ZOOM = 0.25
  const MAX_ZOOM = 1.5

  // Node type discriminators, matching model.NodeRef.Type.
  const INTERNET = 'internet'
  const EXTIP = 'extip'
  const ASSET = 'asset'
  // Not a node type — an account is a container, so it is never a connection
  // endpoint. It is a selection kind because it has settings to edit.
  const ACCOUNT = 'account'

  boot()

  async function boot() {
    const res = await fetch('/api/catalog')
    for (const p of await res.json()) {
      catalog[p.key] = p
    }
    applyPositions(canvas)
    bindEvents()
    updateLines()
  }

  // The server ships the session as JSON alongside the markup it rendered, so
  // load and re-import both land here.
  function readSessionData() {
    const el = document.getElementById('session-data')
    return JSON.parse(el.textContent)
  }

  // Positions live in data attributes rather than inline styles so that the
  // server never has to emit CSS, and one code path applies them.
  function applyPositions(root) {
    for (const el of root.querySelectorAll('[data-x]')) {
      el.style.left = el.dataset.x + 'px'
      el.style.top = el.dataset.y + 'px'
    }
  }

  function moveNode(el, x, y) {
    el.dataset.x = x
    el.dataset.y = y
    el.style.left = x + 'px'
    el.style.top = y + 'px'
  }

  // ---- state lookups -------------------------------------------------------

  function nodeKey(n) {
    if (n.type === INTERNET) return INTERNET
    if (n.type === EXTIP) return EXTIP + ':' + n.id
    return ASSET + ':' + n.accountId + ':' + n.assetId
  }

  function keyToNode(key) {
    if (key === INTERNET) return { type: INTERNET }
    if (key.startsWith(EXTIP + ':')) return { type: EXTIP, id: key.slice(6) }
    const parts = key.split(':')
    return { type: ASSET, accountId: parts[1], assetId: parts[2] }
  }

  function findAccount(id) {
    return state.accounts.find(a => a.id === id)
  }

  function findAsset(accountId, assetId) {
    const acc = findAccount(accountId)
    return acc && acc.assets.find(a => a.id === assetId)
  }

  function findExtIp(id) {
    return state.externalIps.find(e => e.id === id)
  }

  function findConn(id) {
    return state.connections.find(c => c.id === id)
  }

  function nodeExists(n) {
    if (n.type === INTERNET) return true
    if (n.type === EXTIP) return !!findExtIp(n.id)
    return !!findAsset(n.accountId, n.assetId)
  }

  function nodeLabel(n) {
    if (n.type === INTERNET) return 'Internet'
    if (n.type === EXTIP) {
      const e = findExtIp(n.id)
      return e ? e.label + ' (' + e.ip + ')' : '(removed)'
    }
    const acc = findAccount(n.accountId)
    if (!acc) return '(removed)'
    const asset = findAsset(n.accountId, n.assetId)
    return asset ? acc.name + ' → ' + asset.name : acc.name + ' / (removed)'
  }

  // ---- line rendering ------------------------------------------------------

  // A connection touching the internet is classified by direction, because
  // inbound and outbound mean very different things for exposure.
  function internetExposure(conn) {
    if (conn.a.type !== INTERNET && conn.b.type !== INTERNET) return null
    const internetIsA = conn.a.type === INTERNET
    const inbound = internetIsA ? conn.aToB : conn.bToA
    const outbound = internetIsA ? conn.bToA : conn.aToB
    return { internetIsA, inbound, outbound, hasInbound: inbound.length > 0, hasOutbound: outbound.length > 0 }
  }

  function connLinkKind(conn) {
    if (conn.a.type === INTERNET || conn.b.type === INTERNET) return INTERNET
    if (conn.a.type === EXTIP || conn.b.type === EXTIP) return EXTIP
    return 'internal'
  }

  // Line appearance is the whole point of the chart: red means the public
  // internet can initiate a connection inwards.
  function lineStyle(conn) {
    const kind = connLinkKind(conn)
    if (kind === INTERNET) {
      const e = internetExposure(conn)
      if (e.hasInbound) return { color: 'var(--danger)', dash: 'none', width: 2.2 }
      if (e.hasOutbound) return { color: 'var(--warning)', dash: '6,4', width: 1.6 }
      return { color: 'var(--text-faint)', dash: '2,4', width: 1.4 }
    }
    if (kind === EXTIP) {
      const any = conn.aToB.length > 0 || conn.bToA.length > 0
      return { color: 'var(--extip)', dash: any ? '4,3' : '2,4', width: any ? 1.8 : 1.4 }
    }
    return { color: 'var(--accent)', dash: 'none', width: 1.6 }
  }

  // Connector centres are read from the DOM rather than computed, because tile
  // positions come from the account card's flow layout.
  function pointFor(node, canvasRect) {
    const el = canvas.querySelector('[data-node="' + nodeKey(node) + '"]')
    if (!el) return null
    const r = el.getBoundingClientRect()
    // getBoundingClientRect reports visual pixels; the SVG draws in canvas
    // pixels, so undo the zoom.
    return {
      x: (r.left + r.width / 2 - canvasRect.left) / zoom,
      y: (r.top + r.height / 2 - canvasRect.top) / zoom
    }
  }

  function svgEl(name) {
    return document.createElementNS('http://www.w3.org/2000/svg', name)
  }

  function updateLines() {
    const canvasRect = canvas.getBoundingClientRect()
    linesLayer.textContent = ''
    for (const conn of state.connections) {
      const p1 = pointFor(conn.a, canvasRect)
      const p2 = pointFor(conn.b, canvasRect)
      if (!p1 || !p2) continue

      const style = lineStyle(conn)
      const midY = (p1.y + p2.y) / 2
      const c1y = p1.y + Math.max(40, Math.abs(midY - p1.y))
      const c2y = p2.y + Math.max(40, Math.abs(midY - p2.y))
      const d = 'M ' + p1.x + ' ' + p1.y + ' C ' + p1.x + ' ' + c1y + ', ' + p2.x + ' ' + c2y + ', ' + p2.x + ' ' + p2.y
      const selected = selection && selection.kind === 'conn' && selection.id === conn.id

      const g = svgEl('g')

      // A fat transparent path underneath makes a 2px line clickable.
      const hit = svgEl('path')
      hit.setAttribute('d', d)
      hit.setAttribute('class', 'hitpath')
      hit.dataset.connId = conn.id
      g.appendChild(hit)

      const path = svgEl('path')
      path.setAttribute('d', d)
      path.setAttribute('fill', 'none')
      path.setAttribute('stroke', style.color)
      path.setAttribute('stroke-width', selected ? style.width + 0.8 : style.width)
      path.setAttribute('stroke-dasharray', style.dash)
      path.setAttribute('opacity', selected ? '1' : '0.75')
      path.style.pointerEvents = 'none'
      g.appendChild(path)

      g.appendChild(ruleChip(conn, style, (p1.x + p2.x) / 2, (c1y + c2y) / 2))
      linesLayer.appendChild(g)
    }
  }

  // The chip on each line shows direction and how many rules exist, so an
  // unconfigured connection is obvious without opening it.
  function ruleChip(conn, style, x, y) {
    const hasAtoB = conn.aToB.length > 0
    const hasBtoA = conn.bToA.length > 0
    const arrow = hasAtoB && hasBtoA ? '↔' : hasAtoB ? '→' : hasBtoA ? '←' : '–'

    const chip = svgEl('g')
    chip.style.cursor = 'pointer'
    chip.dataset.connId = conn.id

    const rect = svgEl('rect')
    rect.setAttribute('x', x - 17)
    rect.setAttribute('y', y - 7.5)
    rect.setAttribute('width', 34)
    rect.setAttribute('height', 15)
    rect.setAttribute('rx', 4)
    rect.setAttribute('fill', '#0F1319')
    rect.setAttribute('stroke', style.color)
    chip.appendChild(rect)

    const text = svgEl('text')
    text.setAttribute('x', x)
    text.setAttribute('y', y + 3.5)
    text.setAttribute('text-anchor', 'middle')
    text.setAttribute('font-size', '9.5')
    text.setAttribute('font-weight', '600')
    text.setAttribute('fill', style.color)
    text.textContent = arrow + ' ' + (conn.aToB.length + conn.bToA.length)
    chip.appendChild(text)

    return chip
  }

  // ---- dragging ------------------------------------------------------------

  // Drag is entirely local — a round-trip per mousemove would be unusable, and
  // position is the one piece of state the server never needs mid-edit.
  function startDrag(event, el, onMove, onClick) {
    event.preventDefault()
    let lastX = event.clientX
    let lastY = event.clientY
    const originX = event.clientX
    const originY = event.clientY

    function move(ev) {
      onMove((ev.clientX - lastX) / zoom, (ev.clientY - lastY) / zoom)
      lastX = ev.clientX
      lastY = ev.clientY
      updateLines()
    }

    function up(ev) {
      document.removeEventListener('mousemove', move)
      document.removeEventListener('mouseup', up)
      // A drag that never moved is a click.
      if (onClick && Math.abs(ev.clientX - originX) + Math.abs(ev.clientY - originY) < 4) {
        onClick()
      }
    }

    document.addEventListener('mousemove', move)
    document.addEventListener('mouseup', up)
  }

  function dragAccount(event, el) {
    const acc = findAccount(el.dataset.accountId)
    startDrag(event, el, (dx, dy) => {
      acc.x += dx
      acc.y += dy
      moveNode(el, acc.x, acc.y)
    })
  }

  function dragInternet(event, el) {
    startDrag(event, el, (dx, dy) => {
      state.internet.x += dx
      state.internet.y += dy
      moveNode(el, state.internet.x, state.internet.y)
    })
  }

  function dragExtIp(event, el) {
    const e = findExtIp(el.dataset.extipId)
    startDrag(event, el, (dx, dy) => {
      e.x += dx
      e.y += dy
      moveNode(el, e.x, e.y)
    }, () => select({ kind: EXTIP, id: e.id }))
  }

  // ---- connecting ----------------------------------------------------------

  // Click one connector then another. Click-to-click rather than drag-to-drop
  // because connectors are small and the two gestures would fight the drag
  // handler on the node beneath.
  function onConnectorClick(dot) {
    const key = dot.dataset.node
    if (!connectFrom) {
      connectFrom = keyToNode(key)
      dot.classList.add('connecting')
      return
    }
    const fromKey = nodeKey(connectFrom)
    connectFrom = null
    clearConnecting()
    if (fromKey === key) return

    const existing = state.connections.find(c => {
      const pair = [nodeKey(c.a), nodeKey(c.b)]
      return pair.includes(fromKey) && pair.includes(key)
    })
    if (existing) {
      select({ kind: 'conn', id: existing.id })
      return
    }

    const conn = { id: localId('conn'), a: keyToNode(fromKey), b: keyToNode(key), aToB: [], bToA: [] }
    state.connections.push(conn)
    updateLines()
    select({ kind: 'conn', id: conn.id })
  }

  function clearConnecting() {
    for (const el of canvas.querySelectorAll('.connector.connecting')) {
      el.classList.remove('connecting')
    }
  }

  // Connections are never rendered by the server, so their ids are minted here.
  // Random, to stay collision-free against ids from an imported session.
  function localId(prefix) {
    return prefix + '_' + crypto.randomUUID().slice(0, 16).replace(/-/g, '')
  }

  // ---- selection -----------------------------------------------------------

  function select(next) {
    selection = next
    for (const el of canvas.querySelectorAll('.selected')) {
      el.classList.remove('selected')
    }
    if (selection && selection.kind === ASSET) {
      const el = canvas.querySelector('.asset[data-asset-id="' + selection.assetId + '"]')
      if (el) el.classList.add('selected')
    }
    if (selection && selection.kind === EXTIP) {
      const el = canvas.querySelector('.extip-node[data-extip-id="' + selection.id + '"]')
      if (el) el.classList.add('selected')
    }
    if (selection && selection.kind === ACCOUNT) {
      const el = canvas.querySelector('.account[data-account-id="' + selection.accountId + '"]')
      if (el) el.classList.add('selected')
    }
    updateLines()
    openDrawer()
  }

  function clearSelection() {
    selection = null
    for (const el of canvas.querySelectorAll('.selected')) {
      el.classList.remove('selected')
    }
    document.getElementById('drawer').classList.add('hidden')
    updateLines()
  }

  // ---- adding and removing -------------------------------------------------

  async function addAccount(name, provider) {
    const n = state.accounts.length
    const x = 60 + (n % 3) * 380
    const y = 120 + Math.floor(n / 3) * 300
    const html = await postForHTML('/api/render/account', { id: '', name, provider, x, y, assets: [] })
    const el = insert(html)
    const params = {}
    for (const f of catalog[provider].accountParams || []) {
      params[f.key] = f.default
    }
    state.accounts.push({ id: el.dataset.accountId, name, provider, x, y, params, assets: [] })
  }

  async function addExtIp(label, ip) {
    const n = state.externalIps.length
    const x = 1180
    const y = 40 + n * 110
    const html = await postForHTML('/api/render/extip', { id: '', label, ip, x, y })
    const el = insert(html)
    // The server normalises a bare address to CIDR form, so read back what it
    // actually rendered rather than what was typed.
    state.externalIps.push({
      id: el.dataset.extipId,
      label: el.querySelector('.extip-label').textContent,
      ip: el.querySelector('.extip-ip').textContent,
      x, y
    })
    updateLines()
  }

  async function addAsset(accountEl, code) {
    const acc = findAccount(accountEl.dataset.accountId)
    const type = catalog[acc.provider].types.find(t => t.code === code)
    // Repeated types get numbered, so two droplets are tellable apart at a glance.
    const count = acc.assets.filter(a => a.code === code).length + 1
    const name = count > 1 ? type.name + ' ' + count : type.name

    const html = await postForHTML('/api/render/asset', { accountId: acc.id, provider: acc.provider, code, name })
    const tile = parseFragment(html)
    accountEl.querySelector('.add-tile').before(tile)

    const params = {}
    for (const f of type.params) {
      params[f.key] = f.default
    }
    acc.assets.push({ id: tile.dataset.assetId, code, name, params })
    updateLines()
  }

  function removeAccount(id) {
    state.accounts = state.accounts.filter(a => a.id !== id)
    dropDanglingConnections()
    canvas.querySelector('.account[data-account-id="' + id + '"]').remove()
    if (selection && (selection.accountId === id || selection.kind === 'conn')) clearSelection()
    updateLines()
  }

  function removeAsset(accountId, assetId) {
    const acc = findAccount(accountId)
    acc.assets = acc.assets.filter(a => a.id !== assetId)
    dropDanglingConnections()
    canvas.querySelector('.asset[data-asset-id="' + assetId + '"]').remove()
    if (selection && (selection.assetId === assetId || selection.kind === 'conn')) clearSelection()
    updateLines()
  }

  function removeExtIp(id) {
    state.externalIps = state.externalIps.filter(e => e.id !== id)
    dropDanglingConnections()
    canvas.querySelector('.extip-node[data-extip-id="' + id + '"]').remove()
    if (selection && (selection.id === id || selection.kind === 'conn')) clearSelection()
    updateLines()
  }

  // Deleting a node silently deletes the rules that referenced it; leaving a
  // dangling connection would fail import validation later.
  function dropDanglingConnections() {
    state.connections = state.connections.filter(c => nodeExists(c.a) && nodeExists(c.b))
  }

  function removeConnection(id) {
    state.connections = state.connections.filter(c => c.id !== id)
    clearSelection()
    updateLines()
  }

  // ---- server fragments ----------------------------------------------------

  async function postForHTML(url, body) {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
    const text = await res.text()
    if (!res.ok) throw new Error(text)
    return text
  }

  function parseFragment(html) {
    const tpl = document.createElement('template')
    tpl.innerHTML = html.trim()
    const el = tpl.content.firstElementChild
    applyPositions(tpl.content)
    return el
  }

  function insert(html) {
    const el = parseFragment(html)
    canvas.appendChild(el)
    return el
  }

  // ---- validation ----------------------------------------------------------

  // Runs the real tool server-side. The configuration is regenerated there rather
  // than taken from what is on screen, so nothing the browser holds decides what
  // gets executed.
  async function runValidation() {
    const panel = document.getElementById('validate-result')
    const button = document.getElementById('btn-validate')

    panel.className = 'validate-result pending'
    panel.textContent = 'Running init and validate... the first run downloads providers and can take a minute.'
    button.classList.add('busy')

    try {
      const res = await fetch('/api/validate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(state)
      })
      const text = await res.text()
      if (!res.ok) throw new Error(text)

      const result = JSON.parse(text)
      panel.className = 'validate-result ' + (result.ok ? 'ok' : 'bad')
      panel.textContent = validationMessage(result)
    } catch (err) {
      panel.className = 'validate-result bad'
      panel.textContent = 'Could not run validation:\n\n' + err.message
    } finally {
      button.classList.remove('busy')
    }
  }

  // The generated configuration is already in the page, so this copies what the
  // user is looking at rather than asking the server to generate it a second time.
  async function copyGeneratedTF() {
    const label = document.querySelector('#btn-copy-tf [data-copy-label]')
    try {
      await navigator.clipboard.writeText(document.getElementById('generate-output').textContent)
    } catch (err) {
      // Clipboard access can be refused outright, and a silent no-op would read
      // as a copy that worked.
      label.textContent = 'Press Ctrl+C'
      window.getSelection().selectAllChildren(document.getElementById('generate-output'))
      setTimeout(() => { label.textContent = 'Copy' }, 2400)
      return
    }
    label.textContent = 'Copied'
    setTimeout(() => { label.textContent = 'Copy' }, 1200)
  }

  function validationMessage(result) {
    const tool = result.tool || 'validate'
    if (result.ok) {
      return '\u2713 ' + tool + ': configuration is valid.'
    }
    return '\u2717 ' + tool + ' reported problems:\n\n' + result.output
  }

  // ---- import and export ---------------------------------------------------

  function exportSession() {
    const blob = new Blob([JSON.stringify(state, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'infrachart-session.json'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  async function importSession(file) {
    let html
    try {
      html = await postForHTML('/api/render/canvas', JSON.parse(await file.text()))
    } catch (err) {
      window.alert('Could not import that file:\n\n' + err.message)
      return
    }
    clearSelection()
    canvas.innerHTML = html
    linesLayer = document.getElementById('lines-layer')
    state = readSessionData()
    applyPositions(canvas)
    updateLines()
  }

  // ---- drawer --------------------------------------------------------------

  // The whole panel is rendered by the server: node labels, exposure
  // classification and the parameter form all come from Go, so none of that
  // logic exists twice.
  async function openDrawer() {
    const drawer = document.getElementById('drawer')
    if (!selection) {
      drawer.classList.add('hidden')
      return
    }
    // Scopes the provider colour custom properties for the panel's flag badge.
    const acc = selection.accountId ? findAccount(selection.accountId) : null
    drawer.dataset.provider = acc ? acc.provider : ''
    try {
      drawer.innerHTML = await postForHTML('/api/render/drawer', { session: state, selection })
    } catch (err) {
      window.alert('Could not open that item:\n\n' + err.message)
      return
    }
    drawer.classList.remove('hidden')
  }

  function selectedAsset() {
    return selection && selection.kind === ASSET ? findAsset(selection.accountId, selection.assetId) : null
  }

  function selectedRules(dir) {
    const conn = selection && selection.kind === 'conn' ? findConn(selection.id) : null
    return conn ? (dir === 'aToB' ? conn.aToB : conn.bToA) : null
  }

  // Field edits write straight into state and redraw. They deliberately do not
  // re-render the panel — that would steal focus mid-keystroke.
  function onDrawerInput(event) {
    const target = event.target

    const paramKey = target.dataset.param
    if (paramKey) {
      // An account and an asset both render through paramField, so the owner of
      // the field is whichever the drawer currently has selected.
      const owner = selection && selection.kind === ACCOUNT ? findAccount(selection.accountId) : selectedAsset()
      if (!owner) return
      owner.params[paramKey] = readParam(target)
      // A directive that gates other fields changes which fields exist, so the
      // panel has to be rebuilt. Ordinary edits deliberately do not re-render —
      // that would steal focus mid-keystroke — but a gate is only ever a
      // checkbox, so there is no keystroke to interrupt.
      if (gatesOtherParams(paramKey)) openDrawer()
      return
    }

    const ruleField = target.dataset.ruleField
    if (ruleField) {
      const row = target.closest('.rule-row')
      const rules = selectedRules(row.dataset.dir)
      if (rules) {
        rules[Number(row.dataset.index)][ruleField] = target.value
        updateLines()
      }
      return
    }

    if (target.id === 'asset-display-name') {
      const asset = selectedAsset()
      if (!asset) return
      asset.name = target.value
      canvas.querySelector('.asset[data-asset-id="' + asset.id + '"] .asset-name').textContent = target.value
      updateLines()
      return
    }

    if (target.id === 'account-display-name') {
      const acc = findAccount(selection.accountId)
      if (!acc) return
      acc.name = target.value
      canvas.querySelector('.account[data-account-id="' + acc.id + '"] .account-name').textContent = target.value
      updateLines()
      return
    }

    if (target.id === 'extip-label-input' || target.id === 'extip-ip-input') {
      const e = findExtIp(selection.id)
      if (!e) return
      const node = canvas.querySelector('.extip-node[data-extip-id="' + e.id + '"]')
      if (target.id === 'extip-label-input') {
        e.label = target.value
        node.querySelector('.extip-label').textContent = e.label
      } else {
        // Left exactly as typed; the server normalises it to CIDR form on
        // import or generation, and rejects it there if it is malformed.
        e.ip = target.value
        node.querySelector('.extip-ip').textContent = e.ip
      }
      updateLines()
    }
  }

  // Whether any field in the selected item's form is gated on this one. Read from
  // the catalog rather than a hardcoded list, so a new gate needs no change here.
  function gatesOtherParams(paramKey) {
    let fields = null
    if (selection && selection.kind === ACCOUNT) {
      const acc = findAccount(selection.accountId)
      fields = acc && catalog[acc.provider].accountParams
    } else {
      const asset = selectedAsset()
      const acc = asset && findAccount(selection.accountId)
      const type = acc && catalog[acc.provider].types.find(t => t.code === asset.code)
      fields = type && type.params
    }
    return (fields || []).some(f => f.requiresParam === paramKey)
  }

  function readParam(el) {
    if (el.type === 'checkbox') return el.checked
    if (el.dataset.paramType === 'number') return Number(el.value)
    return el.value
  }

  // Adding or removing a rule changes the panel's structure, so it is the one
  // case that re-renders.
  function onDrawerClick(event) {
    if (event.target.closest('#btn-close-drawer')) {
      clearSelection()
      return
    }
    if (event.target.closest('#btn-delete-item')) {
      deleteSelected()
      return
    }

    const add = event.target.closest('[data-add-rule]')
    if (add) {
      const rules = selectedRules(add.dataset.addRule)
      if (!rules) return
      rules.push({ protocol: 'TCP', port: '', detail: '' })
      updateLines()
      openDrawer()
      return
    }

    const del = event.target.closest('.rule-del')
    if (del) {
      const row = del.closest('.rule-row')
      const rules = selectedRules(row.dataset.dir)
      if (!rules) return
      rules.splice(Number(row.dataset.index), 1)
      updateLines()
      openDrawer()
    }
  }

  function deleteSelected() {
    if (!selection) return
    if (selection.kind === 'conn') removeConnection(selection.id)
    else if (selection.kind === ACCOUNT) removeAccount(selection.accountId)
    else if (selection.kind === ASSET) removeAsset(selection.accountId, selection.assetId)
    else if (selection.kind === EXTIP) removeExtIp(selection.id)
  }

  // ---- events --------------------------------------------------------------

  // Everything is delegated from the document, so fragments inserted later need
  // no wiring of their own.
  function bindEvents() {
    canvas.addEventListener('mousedown', onCanvasMouseDown)
    canvas.addEventListener('click', onCanvasClick)
    canvas.addEventListener('input', onCanvasInput)
    canvas.addEventListener('keydown', onCanvasKeyDown)
    document.addEventListener('click', onDocumentClick)
    const drawer = document.getElementById('drawer')
    drawer.addEventListener('click', onDrawerClick)
    drawer.addEventListener('input', onDrawerInput)
    drawer.addEventListener('change', onDrawerInput)
    window.addEventListener('resize', updateLines)
    canvasWrap.addEventListener('scroll', updateLines)
    bindToolbar()
    bindZoom()
  }

  function onCanvasMouseDown(event) {
    // Connecting is handled on click, not here. Doing it on mousedown meant the
    // click that followed fell through to the node underneath and replaced the
    // new connection in the selection.
    if (event.target.closest('.connector')) return
    if (connectFrom) {
      connectFrom = null
      clearConnecting()
    }
    if (event.target.closest('.account-close, .asset-close, .extip-close, .account-settings')) return
    // The account name is edited in place, so it must not start a drag.
    if (event.target.closest('.account-name')) return

    const header = event.target.closest('.account-header')
    if (header) {
      dragAccount(event, header.closest('.account'))
      return
    }
    const internet = event.target.closest('.internet-node')
    if (internet) {
      dragInternet(event, internet)
      return
    }
    const extip = event.target.closest('.extip-node')
    if (extip) {
      dragExtIp(event, extip)
      return
    }
    startPan(event)
  }

  // Dragging empty canvas pans the view. This scrolls the wrapper rather than
  // moving anything, so nothing in the session changes and the delta needs no
  // zoom conversion — scroll offsets are already in visual pixels.
  function startPan(event) {
    if (event.button !== 0) return
    // Clicking inside a node or a floating menu is never a pan, even where no
    // earlier branch claimed it — the body of an account card, for instance.
    if (event.target.closest('.account, .internet-node, .extip-node, .type-menu')) return

    event.preventDefault()
    let lastX = event.clientX
    let lastY = event.clientY
    canvas.classList.add('panning')

    function move(ev) {
      canvasWrap.scrollLeft -= ev.clientX - lastX
      canvasWrap.scrollTop -= ev.clientY - lastY
      lastX = ev.clientX
      lastY = ev.clientY
    }

    function up() {
      document.removeEventListener('mousemove', move)
      document.removeEventListener('mouseup', up)
      canvas.classList.remove('panning')
    }

    document.addEventListener('mousemove', move)
    document.addEventListener('mouseup', up)
  }

  function onCanvasClick(event) {
    const connector = event.target.closest('.connector')
    if (connector) {
      onConnectorClick(connector)
      return
    }
    const closeAccount = event.target.closest('.account-close')
    if (closeAccount) {
      removeAccount(closeAccount.closest('.account').dataset.accountId)
      return
    }
    const closeAsset = event.target.closest('.asset-close')
    if (closeAsset) {
      const tile = closeAsset.closest('.asset')
      removeAsset(tile.dataset.accountId, tile.dataset.assetId)
      return
    }
    const closeExtIp = event.target.closest('.extip-close')
    if (closeExtIp) {
      removeExtIp(closeExtIp.closest('.extip-node').dataset.extipId)
      return
    }
    const settings = event.target.closest('.account-settings')
    if (settings) {
      select({ kind: ACCOUNT, accountId: settings.closest('.account').dataset.accountId })
      return
    }
    const addTile = event.target.closest('.add-tile')
    if (addTile) {
      event.stopPropagation()
      openTypeMenu(addTile)
      return
    }
    const line = event.target.closest('[data-conn-id]')
    if (line) {
      select({ kind: 'conn', id: line.dataset.connId })
      return
    }
    const tile = event.target.closest('.asset')
    if (tile) {
      select({ kind: ASSET, accountId: tile.dataset.accountId, assetId: tile.dataset.assetId })
    }
  }

  // Renaming happens in place on the card. `plaintext-only` keeps pasted markup
  // out, and the text is read back rather than trusted, because the server
  // rejects control characters and over-long names on import.
  function onCanvasInput(event) {
    const nameEl = event.target.closest('.account-name')
    if (!nameEl) return
    const acc = findAccount(nameEl.closest('.account').dataset.accountId)
    if (acc) acc.name = nameEl.textContent.replace(/[\r\n]+/g, ' ').slice(0, 200)
  }

  // Enter should commit the rename rather than insert a line break.
  function onCanvasKeyDown(event) {
    if (event.key === 'Enter' && event.target.closest('.account-name')) {
      event.preventDefault()
      event.target.blur()
    }
  }

  function onDocumentClick(event) {
    const item = event.target.closest('.type-menu-item')
    if (item) {
      const menu = item.closest('.type-menu')
      addAsset(canvas.querySelector('.account[data-account-id="' + menu.dataset.accountId + '"]'), item.dataset.code)
      menu.classList.add('hidden')
      return
    }
    const close = event.target.closest('[data-close-panel]')
    if (close) {
      document.getElementById(close.dataset.closePanel).classList.add('hidden')
      return
    }
    if (!event.target.closest('.type-menu, .add-tile, .add-account-panel, .add-extip-panel, #btn-add-account, #btn-add-extip')) {
      closeFloatingPanels()
    }
  }

  // ---- zoom ----------------------------------------------------------------

  function setZoom(next) {
    zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, next))
    canvas.style.zoom = zoom
    document.getElementById('zoom-level').textContent = Math.round(zoom * 100) + '%'
    updateLines()
  }

  function bindZoom() {
    document.getElementById('btn-zoom-out').addEventListener('click', () => setZoom(zoom - 0.1))
    document.getElementById('btn-zoom-in').addEventListener('click', () => setZoom(zoom + 0.1))
    document.getElementById('zoom-level').addEventListener('click', () => setZoom(1))

    // Ctrl+scroll is the zoom gesture people already expect, and it is also
    // what a trackpad pinch sends.
    canvasWrap.addEventListener('wheel', event => {
      if (!event.ctrlKey && !event.metaKey) return
      event.preventDefault()
      setZoom(zoom - Math.sign(event.deltaY) * 0.1)
    }, { passive: false })
  }

  function closeFloatingPanels() {
    for (const el of document.querySelectorAll('.type-menu, .add-account-panel, .add-extip-panel')) {
      el.classList.add('hidden')
    }
  }

  // The per-provider menus are pre-rendered; opening one is a position and a
  // class toggle.
  function openTypeMenu(addTile) {
    closeFloatingPanels()
    const account = addTile.closest('.account')
    const provider = account.dataset.provider
    const menu = document.querySelector('.type-menu[data-type-menu="' + provider + '"]')
    if (!menu) return
    const tileRect = addTile.getBoundingClientRect()
    const canvasRect = canvas.getBoundingClientRect()
    menu.dataset.accountId = account.dataset.accountId
    menu.style.left = (tileRect.left - canvasRect.left) / zoom + 'px'
    menu.style.top = (tileRect.bottom - canvasRect.top) / zoom + 6 + 'px'
    menu.classList.remove('hidden')
    canvas.appendChild(menu)
  }

  function bindToolbar() {
    const accountPanel = document.getElementById('add-account-panel')
    const extIpPanel = document.getElementById('add-extip-panel')

    document.getElementById('btn-add-account').addEventListener('click', () => {
      const hidden = accountPanel.classList.contains('hidden')
      closeFloatingPanels()
      if (hidden) accountPanel.classList.remove('hidden')
    })

    document.getElementById('btn-add-extip').addEventListener('click', () => {
      const hidden = extIpPanel.classList.contains('hidden')
      closeFloatingPanels()
      if (hidden) extIpPanel.classList.remove('hidden')
    })

    // Suggest a name that matches the chosen provider and does not collide.
    document.getElementById('new-acc-provider').addEventListener('change', event => {
      const provider = event.target.value
      const count = state.accounts.filter(a => a.provider === provider).length + 1
      document.getElementById('new-acc-name').value = catalog[provider].label + ' Account ' + count
    })

    document.getElementById('confirm-add-acc').addEventListener('click', async () => {
      const name = document.getElementById('new-acc-name').value.trim() || 'New account'
      await addAccount(name, document.getElementById('new-acc-provider').value)
      accountPanel.classList.add('hidden')
    })

    document.getElementById('confirm-add-extip').addEventListener('click', async () => {
      const label = document.getElementById('new-extip-label').value.trim() || 'External IP'
      const ip = document.getElementById('new-extip-ip').value.trim()
      try {
        await addExtIp(label, ip)
      } catch (err) {
        window.alert(err.message)
        return
      }
      extIpPanel.classList.add('hidden')
    })

    document.getElementById('btn-generate').addEventListener('click', async () => {
      const modal = document.getElementById('generate-modal')
      const output = document.getElementById('generate-output')
      // A verdict from a previous generation would be about different config.
      document.getElementById('validate-result').className = 'validate-result hidden'
      output.textContent = 'Generating...'
      modal.classList.remove('hidden')
      try {
        output.textContent = await postForHTML('/api/generate', state)
      } catch (err) {
        output.textContent = 'Could not generate:\n\n' + err.message
      }
    })

    document.getElementById('btn-copy-tf').addEventListener('click', copyGeneratedTF)
    document.getElementById('btn-validate').addEventListener('click', runValidation)

    document.getElementById('btn-export').addEventListener('click', exportSession)

    const importFile = document.getElementById('import-file')
    document.getElementById('btn-import').addEventListener('click', () => importFile.click())
    importFile.addEventListener('change', () => {
      if (importFile.files.length) importSession(importFile.files[0])
      importFile.value = ''
    })

  }

})()
