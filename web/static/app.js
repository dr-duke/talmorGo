/* ── TalmorGo app.js ── */
'use strict';

/* ── Helpers ── */
function base() {
  return document.querySelector('base')?.href || '/';
}

/* ── Toast ── */
let toastTimer;
function showToast(msg) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.classList.add('visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('visible'), 2800);
}

document.body.addEventListener('showToast', (e) => {
  showToast(e.detail?.value || '');
});

/* ── Tab switching ── */
function switchTab(tab) {
  const libSection = document.getElementById('lib-section');
  const queueSection = document.getElementById('queue-section');
  const libBtn = document.getElementById('tab-lib-btn');
  const queueBtn = document.getElementById('tab-queue-btn');
  if (!libSection || !queueSection) return;
  libSection.style.display = tab === 'lib' ? '' : 'none';
  queueSection.style.display = tab === 'queue' ? '' : 'none';
  libBtn.classList.toggle('active', tab === 'lib');
  queueBtn.classList.toggle('active', tab === 'queue');
}

/* ── Filter state ── */
const filter = { q: '', kind: '', tag: '' };

function applyFilter() {
  const fq = document.getElementById('filter-q');
  const fk = document.getElementById('filter-kind');
  const ft = document.getElementById('filter-tag');
  if (fq) fq.value = filter.q;
  if (fk) fk.value = filter.kind;
  if (ft) ft.value = filter.tag;
  const inner = document.getElementById('media-inner');
  if (inner) htmx.trigger(inner, 'mediaRefresh');
}

function setKind(btn, kind) {
  filter.kind = kind;
  filter.tag = '';
  hideSidebarColl();
  document.querySelectorAll('.sidebar-nav-item[data-kind]').forEach(b => {
    b.classList.toggle('active', b.dataset.kind === kind);
  });
  hidePlayAll();
  applyFilter();
}

function setColl(btn, name) {
  filter.tag = name;
  filter.kind = '';
  document.querySelectorAll('.sidebar-nav-item[data-kind]').forEach(b => b.classList.remove('active'));
  document.querySelectorAll('.sidebar-nav-item[data-coll]').forEach(b => {
    b.classList.toggle('active', b.dataset.coll === name);
  });
  showPlayAll(name);
  applyFilter();
}

function hideSidebarColl() {
  document.querySelectorAll('.sidebar-nav-item[data-coll]').forEach(b => b.classList.remove('active'));
}

function onSearch(val) {
  filter.q = val;
  applyFilter();
}

/* ── Play-all bar ── */
function showPlayAll(name) {
  const bar = document.getElementById('play-all-bar');
  const title = document.getElementById('play-all-title');
  if (!bar) return;
  if (title) title.textContent = name;
  bar.classList.add('visible');
}

function hidePlayAll() {
  const bar = document.getElementById('play-all-bar');
  if (bar) bar.classList.remove('visible');
}

/* ── Tag cloud ── */
function toggleTag(btn) {
  const tag = btn.dataset.tag;
  if (filter.tag === tag) {
    filter.tag = '';
    btn.classList.remove('active');
    hideSidebarColl();
    document.querySelectorAll('.sidebar-nav-item[data-kind=""]').forEach(b => b.classList.add('active'));
    hidePlayAll();
  } else {
    filter.tag = tag;
    document.querySelectorAll('#tag-cloud .chip').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    if (btn.classList.contains('coll')) {
      showPlayAll(tag);
      const collBtn = document.querySelector(`.sidebar-nav-item[data-coll="${CSS.escape(tag)}"]`);
      if (collBtn) {
        document.querySelectorAll('.sidebar-nav-item').forEach(b => b.classList.remove('active'));
        collBtn.classList.add('active');
      }
    } else {
      hidePlayAll();
    }
  }
  const ft = document.getElementById('filter-tag');
  if (ft) ft.value = filter.tag;
  const inner = document.getElementById('media-inner');
  if (inner) htmx.trigger(inner, 'mediaRefresh');
}

function expandTags() {
  const cloud = document.getElementById('tag-cloud');
  if (cloud) cloud.classList.add('tag-cloud-expanded');
}

/* ── Row menu ── */
let openMenu = null;

function toggleMenu(evt, btn) {
  evt.stopPropagation();
  const menu = btn.closest('.row-menu-wrap').querySelector('.row-menu');
  if (openMenu && openMenu !== menu) {
    openMenu.classList.remove('open');
  }
  menu.classList.toggle('open');
  openMenu = menu.classList.contains('open') ? menu : null;
}

document.addEventListener('click', (e) => {
  if (openMenu) { openMenu.classList.remove('open'); openMenu = null; }
  if (collDropOpen && !e.target.closest('.coll-dropdown-wrap')) closeCollDropdown();
});

/* ════════════════════════════════════════════════════
   UNIFIED PLAYER
   ════════════════════════════════════════════════════ */

let plyrPlayer   = null;
let playerKind   = null;   // 'video' | 'audio' | null
let _minimizing  = false;  // flag: dialog close triggered by playerMinimize()

/* ── Playlist (video sequential) ── */
let playlist = [];
let playlistIndex = -1;

function buildPlaylist() {
  return Array.from(
    document.querySelectorAll('#media-inner .media-row[data-stream][data-kind="video"]')
  ).map(row => ({ stream: row.dataset.stream, title: row.dataset.title }));
}

/* ── Entry points ── */

function rowActivate(evt, row) {
  if (evt) evt.stopPropagation();
  if (!row || !row.dataset.stream) return;
  openMedia(row.dataset.stream, row.dataset.title, row.dataset.kind || 'video');
}

/* kept for legacy calls */
function rowPlay(evt, row) {
  if (evt) evt.stopPropagation();
  if (!row || !row.dataset.stream) return;
  openMedia(row.dataset.stream, row.dataset.title, 'video');
}

function playAll() {
  playlist = buildPlaylist();
  playlistIndex = 0;
  if (playlist.length > 0) openMedia(playlist[0].stream, playlist[0].title, 'video');
}

function playNext() {
  if (playlistIndex >= 0 && playlistIndex < playlist.length - 1) {
    playlistIndex++;
    openMedia(playlist[playlistIndex].stream, playlist[playlistIndex].title, 'video');
  }
}

/* ── Teardown current player (before switching kind or stopping) ── */
function _teardown() {
  if (playerKind === 'video') {
    const dlg = document.getElementById('player-dialog');
    if (dlg && dlg.open) {
      // _minimizing suppresses the close-event handler calling _barShow()
      // (close fires synchronously inside dlg.close())
      _minimizing = true;
      dlg.close();
      _minimizing = false;
    }
    if (plyrPlayer) { try { plyrPlayer.pause(); plyrPlayer.destroy(); } catch(e) {} plyrPlayer = null; }
    const video = document.getElementById('main-player');
    if (video) video.src = '';
  } else if (playerKind === 'audio') {
    const a = document.getElementById('audio-player');
    if (a) { a.pause(); a.src = ''; }
  }
}

/* ── Core open ── */
function openMedia(stream, title, kind) {
  // Stop the other kind before switching (prevents two players running at once)
  if (playerKind !== null && playerKind !== kind) _teardown();

  playerKind = kind;

  // update bar meta
  const pbTitle = document.getElementById('pb-title');
  const pbIcon  = document.getElementById('pb-kind-icon');
  if (pbTitle) pbTitle.textContent = title || '';
  if (pbIcon)  pbIcon.textContent = kind === 'audio' ? 'audio_file' : 'movie';

  if (kind === 'video') {
    // build/update playlist context
    if (!playlist.length) { playlist = buildPlaylist(); }
    const idx = playlist.findIndex(p => p.stream === stream);
    playlistIndex = idx >= 0 ? idx : 0;
    _openVideo(stream, title);
  } else {
    _openAudio(stream, title);
  }

  _barShow();
}

/* ── Video ── */
function _openVideo(stream, title) {
  const dlg   = document.getElementById('player-dialog');
  const video = document.getElementById('main-player');
  const titleEl = document.getElementById('player-title');
  if (!dlg || !video) return;

  if (titleEl) titleEl.textContent = title || '';

  // Destroy old Plyr before changing src
  if (plyrPlayer) {
    try { plyrPlayer.destroy(); } catch(e) {}
    plyrPlayer = null;
  }

  video.src = stream;
  // showModal() throws if dialog is already open (e.g. clicking another video while one plays)
  if (!dlg.open) dlg.showModal();

  plyrPlayer = new Plyr(video, {
    autoplay: true,
    controls: ['play-large','play','progress','current-time','mute','volume','captions','fullscreen'],
    keyboard: { focused: true, global: false },
    fullscreen: { enabled: true, fallback: true, iosNative: false },
  });

  // Call play() immediately within user gesture window (not deferred to 'ready' event)
  plyrPlayer.play().catch(() => {});

  plyrPlayer.on('ended', () => setTimeout(playNext, 600));
  plyrPlayer.on('play',  () => _pbPlayIcon(true));
  plyrPlayer.on('pause', () => _pbPlayIcon(false));
  plyrPlayer.on('timeupdate', _updateVideoProgress);
}

/* ── Audio ── */
function _openAudio(stream, title) {
  const audio = document.getElementById('audio-player');
  if (!audio) return;
  audio.src = stream;
  audio.play().catch(() => {});
}

/* ── Expand / Minimize ── */
function playerExpand() {
  if (playerKind !== 'video') return;
  const dlg = document.getElementById('player-dialog');
  if (dlg && !dlg.open) dlg.showModal();
}

function playerMinimize() {
  const dlg = document.getElementById('player-dialog');
  if (!dlg || !dlg.open) return;
  _minimizing = true;
  dlg.close();
  _minimizing = false;
  _barShow();
}

/* ── Toggle play/pause ── */
function playerToggle() {
  if (playerKind === 'video' && plyrPlayer) {
    if (plyrPlayer.paused) plyrPlayer.play(); else plyrPlayer.pause();
  } else if (playerKind === 'audio') {
    const a = document.getElementById('audio-player');
    if (a) { if (a.paused) a.play(); else a.pause(); }
  }
}

/* ── Full stop ── */
function playerClose() {
  const kind = playerKind;
  // Clear playerKind BEFORE dlg.close() — the 'close' event fires asynchronously
  // (browser queues it as a task), so by the time it fires _closing would already
  // be reset. Instead we check playerKind===null in the handler.
  playerKind = null;
  playlist = []; playlistIndex = -1;

  if (kind === 'video') {
    const dlg = document.getElementById('player-dialog');
    if (dlg && dlg.open) dlg.close();
    if (plyrPlayer) {
      try { plyrPlayer.pause(); plyrPlayer.destroy(); } catch(e) {}
      plyrPlayer = null;
    }
    const video = document.getElementById('main-player');
    if (video) { video.src = ''; }
  } else if (kind === 'audio') {
    const a = document.getElementById('audio-player');
    if (a) { a.pause(); a.src = ''; }
  }

  _barHide();
}

/* legacy aliases */
function closePlayer() { playerClose(); }
function closeAudio()  { playerClose(); }
function openVideo(stream, title) { openMedia(stream, title, 'video'); }
function openAudio(stream, title) { openMedia(stream, title, 'audio'); }

/* ── Dialog events ── */
document.getElementById('player-dialog')?.addEventListener('cancel', (e) => {
  // Escape key → minimize instead of close
  e.preventDefault();
  playerMinimize();
});

document.getElementById('player-dialog')?.addEventListener('close', () => {
  // 'close' event is async (browser queues it); by the time it fires:
  //   - playerMinimize(): _minimizing may already be false → check _minimizing first
  //   - playerClose():    playerKind is already null  → check playerKind
  if (_minimizing || playerKind === null) return;
  // Implicit close (backdrop click) → treat as minimize
  _barShow();
});

/* ── Bottom bar show/hide ── */
function _barShow() {
  const bar = document.getElementById('player-bar');
  if (bar) bar.classList.add('visible');
  document.body.classList.add('has-player');

  const expandBtn = document.getElementById('pb-expand-btn');
  if (expandBtn) expandBtn.style.display = playerKind === 'video' ? '' : 'none';
}

function _barHide() {
  const bar = document.getElementById('player-bar');
  if (bar) bar.classList.remove('visible');
  document.body.classList.remove('has-player');
  _resetProgress();
}

/* ── Progress bar ── */
function _pbPlayIcon(playing) {
  const icon = document.getElementById('pb-play-icon');
  if (icon) icon.textContent = playing ? 'pause' : 'play_arrow';
}

function _fmtTime(s) {
  if (!isFinite(s) || s < 0) return '0:00';
  const m = Math.floor(s / 60);
  return m + ':' + String(Math.floor(s % 60)).padStart(2, '0');
}

function _setProgress(cur, dur) {
  const fill  = document.getElementById('pb-fill');
  const curEl = document.getElementById('pb-current');
  const durEl = document.getElementById('pb-duration');
  const pct   = dur > 0 ? Math.min(100, (cur / dur) * 100) : 0;
  if (fill)  fill.style.width = pct + '%';
  if (curEl) curEl.textContent = _fmtTime(cur);
  if (durEl) durEl.textContent = _fmtTime(dur);
}

function _resetProgress() {
  _setProgress(0, 0);
  _pbPlayIcon(false);
}

function _updateVideoProgress() {
  if (!plyrPlayer) return;
  _setProgress(plyrPlayer.currentTime, plyrPlayer.duration);
}

/* Seek on click / drag */
function playerSeek(evt) {
  const track = document.getElementById('pb-track');
  if (!track) return;
  const rect = track.getBoundingClientRect();
  const pct  = Math.max(0, Math.min(1, (evt.clientX - rect.left) / rect.width));
  if (playerKind === 'video' && plyrPlayer && plyrPlayer.duration) {
    plyrPlayer.currentTime = plyrPlayer.duration * pct;
  } else if (playerKind === 'audio') {
    const a = document.getElementById('audio-player');
    if (a && a.duration) a.currentTime = a.duration * pct;
  }
}

/* Also support drag-to-seek on progress bar */
(function() {
  const track = document.getElementById('pb-track');
  if (!track) return;
  let dragging = false;
  track.addEventListener('mousedown', () => { dragging = true; });
  document.addEventListener('mousemove', (e) => { if (dragging) playerSeek(e); });
  document.addEventListener('mouseup',   () => { dragging = false; });
  track.addEventListener('touchmove', (e) => {
    e.preventDefault();
    const t = e.touches[0];
    playerSeek(t);
  }, { passive: false });
})();

/* ── Audio element: persistent event listeners ── */
(function initAudio() {
  const a = document.getElementById('audio-player');
  if (!a) return;
  a.addEventListener('timeupdate', () => {
    if (playerKind === 'audio') _setProgress(a.currentTime, a.duration);
  });
  a.addEventListener('durationchange', () => {
    if (playerKind === 'audio') _setProgress(a.currentTime, a.duration);
  });
  a.addEventListener('play',  () => { if (playerKind === 'audio') _pbPlayIcon(true);  });
  a.addEventListener('pause', () => { if (playerKind === 'audio') _pbPlayIcon(false); });
  a.addEventListener('ended', () => { if (playerKind === 'audio') _pbPlayIcon(false); });
})();

/* ════════════════════════════════════════════════════
   LOG DIALOG
   ════════════════════════════════════════════════════ */
function openLog(jobId, title) {
  const dlg     = document.getElementById('log-dialog');
  const titleEl = document.getElementById('log-title');
  const content = document.getElementById('log-content');
  if (!dlg) return;
  if (titleEl) titleEl.textContent = 'Лог: ' + (title || jobId);
  if (content) content.textContent = 'Загрузка…';
  dlg.showModal();
  fetch(base() + 'jobs/' + jobId + '/log')
    .then(r => r.text())
    .then(t => { if (content) content.textContent = t || '(пусто)'; })
    .catch(e => { if (content) content.textContent = 'Ошибка: ' + e; });
}

/* ── Dialogs: close on backdrop click ── */
document.addEventListener('click', (e) => {
  if (e.target.tagName === 'DIALOG' && e.target.id !== 'player-dialog') {
    e.target.close();
  }
});

/* ── Copy helpers ── */
function copyLink(itemId) {
  fetch(base() + 'items/' + itemId + '/link', { method: 'POST' })
    .then(r => r.json())
    .then(d => { navigator.clipboard.writeText(d.url); showToast('Ссылка скопирована'); })
    .catch(() => showToast('Ошибка получения ссылки'));
}

function copyURL(url) {
  navigator.clipboard.writeText(url).then(() => showToast('URL скопирован'));
}

/* ── Rename ── */
function renameFile(itemId, currentName) {
  const newName = window.prompt('Новое имя файла:', currentName);
  if (!newName || newName === currentName) return;
  fetch(base() + 'items/' + itemId, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: newName })
  }).then(r => {
    if (r.ok) { showToast('Переименовано'); const mi = document.getElementById('media-inner'); if (mi) htmx.trigger(mi, 'mediaRefresh'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

/* ── Delete file ── */
function deleteFile(itemId) {
  fetch(base() + 'items/' + itemId, { method: 'DELETE' })
    .then(r => {
      if (r.ok) { showToast('Файл удалён'); const mi = document.getElementById('media-inner'); if (mi) htmx.trigger(mi, 'mediaRefresh'); }
      else r.text().then(t => showToast('Ошибка: ' + t));
    });
}

/* ── Add tag ── */
function addTag(jobId) {
  const name = window.prompt('Имя тега:');
  if (!name) return;
  fetch(base() + 'jobs/' + jobId + '/tags', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name })
  }).then(r => {
    if (r.ok) { const mi = document.getElementById('media-inner'); if (mi) htmx.trigger(mi, 'mediaRefresh'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

/* ── Bulk selection ── */
const selectedJobs = new Set();

function onRowSelect(cb) {
  if (cb.checked) selectedJobs.add(cb.value);
  else selectedJobs.delete(cb.value);
  updateActionBar();
}

function updateActionBar() {
  const bar   = document.getElementById('action-bar');
  const count = document.getElementById('select-count');
  if (!bar) return;
  const n = selectedJobs.size;
  if (count) count.textContent = n + ' выбрано';
  bar.classList.toggle('hidden', n === 0);
}

function clearSelection() {
  selectedJobs.clear();
  document.querySelectorAll('.row-checkbox:checked').forEach(cb => { cb.checked = false; });
  updateActionBar();
}

function bulkTag() {
  const name = window.prompt('Тег для всех выбранных:');
  if (!name || !selectedJobs.size) return;
  fetch(base() + 'media/bulk-tag', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tag: name, job_ids: [...selectedJobs] })
  }).then(r => {
    if (r.ok) { clearSelection(); const mi = document.getElementById('media-inner'); if (mi) htmx.trigger(mi, 'mediaRefresh'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

function bulkHide() {
  if (!selectedJobs.size) return;
  fetch(base() + 'media/bulk-hide', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ job_ids: [...selectedJobs] })
  }).then(r => {
    if (r.ok) { clearSelection(); const mi = document.getElementById('media-inner'); if (mi) htmx.trigger(mi, 'mediaRefresh'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

/* ── Collection dropdown (bulk) ── */
let collDropOpen = false;

function closeCollDropdown() {
  const dd = document.getElementById('coll-dropdown');
  if (dd) dd.classList.add('hidden');
  collDropOpen = false;
}

function renderCollDropdown(cols) {
  const dd = document.getElementById('coll-dropdown');
  if (!dd) return;
  const items = cols.map(c =>
    `<button class="row-menu-item" onclick="addToCollection('${c.ID}')">${c.Name}</button>`
  ).join('');
  const createBtn = `<div class="row-menu-divider"></div>
    <button class="row-menu-item" onclick="createAndAddToCollection()">
      <span class="mi">create_new_folder</span>Создать коллекцию…
    </button>`;
  dd.innerHTML = items + createBtn;
}

function toggleCollDropdown() {
  const dd = document.getElementById('coll-dropdown');
  if (!dd) return;
  collDropOpen = !collDropOpen;
  dd.classList.toggle('hidden', !collDropOpen);
  if (collDropOpen) {
    dd.innerHTML = '<div style="padding:.5rem;color:var(--text-2);font-size:.8rem">Загрузка…</div>';
    fetch(base() + 'collections')
      .then(r => r.json())
      .then(cols => renderCollDropdown(cols))
      .catch(() => { dd.innerHTML = '<div style="padding:.5rem;color:var(--danger)">Ошибка</div>'; });
  }
}

function addToCollection(collId) {
  if (!selectedJobs.size) return;
  closeCollDropdown();
  fetch(base() + 'collections/' + collId + '/jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ job_ids: [...selectedJobs] })
  }).then(r => {
    if (r.ok) { clearSelection(); const mi = document.getElementById('media-inner'); if (mi) htmx.trigger(mi, 'mediaRefresh'); showToast('Добавлено в коллекцию'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

async function createAndAddToCollection() {
  closeCollDropdown();
  const name = window.prompt('Название новой коллекции:');
  if (!name) return;
  const r = await fetch(base() + 'collections', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name })
  });
  if (!r.ok) { showToast('Ошибка создания коллекции'); return; }
  const col = await r.json();
  addToCollection(col.ID);
}

/* ── Status filter ── */
function activateStatusFilter(status) {
  filter.tag = status;
  filter.kind = '';
  const ft = document.getElementById('filter-tag');
  if (ft) ft.value = status;
  applyFilter();
}

/* ── Collection sidebar refresh ── */
document.body.addEventListener('collectionsRefresh', () => {
  const sidebar = document.getElementById('sidebar');
  if (sidebar) htmx.ajax('GET', 'library/sidebar', { target: '#sidebar', swap: 'outerHTML' });
});
