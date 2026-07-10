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

/* ── HX-Trigger: showToast event ── */
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

document.addEventListener('click', () => {
  if (openMenu) {
    openMenu.classList.remove('open');
    openMenu = null;
  }
});

/* ── Sequential playlist ── */
let playlist = [];
let playlistIndex = -1;

function buildPlaylist() {
  return Array.from(document.querySelectorAll('#media-inner .media-row[data-stream][data-kind="video"]'))
    .map(row => ({ stream: row.dataset.stream, title: row.dataset.title }));
}

function rowPlay(evt, row) {
  if (evt) evt.stopPropagation();
  if (!row || !row.dataset.stream) return;
  playlist = buildPlaylist();
  playlistIndex = playlist.findIndex(p => p.stream === row.dataset.stream);
  if (playlistIndex < 0) playlistIndex = 0;
  openVideo(row.dataset.stream, row.dataset.title);
}

function playAll() {
  playlist = buildPlaylist();
  playlistIndex = 0;
  if (playlist.length > 0) openVideo(playlist[0].stream, playlist[0].title);
}

function playNext() {
  if (playlistIndex >= 0 && playlistIndex < playlist.length - 1) {
    playlistIndex++;
    openVideo(playlist[playlistIndex].stream, playlist[playlistIndex].title);
  }
}

/* ── Video player (Plyr) ── */
let player = null;

function openVideo(streamUrl, title) {
  const dlg = document.getElementById('player-dialog');
  const titleEl = document.getElementById('player-title');
  const video = document.getElementById('main-player');
  if (!dlg || !video) return;
  if (titleEl) titleEl.textContent = title || '';
  if (player) { try { player.destroy(); } catch(e) {} player = null; }
  video.src = streamUrl;
  dlg.showModal();
  player = new Plyr(video, {
    controls: ['play-large','play','progress','current-time','mute','volume','captions','fullscreen'],
    keyboard: { focused: true, global: false },
    fullscreen: { enabled: true, fallback: true, iosNative: false }
  });
  player.on('ended', () => setTimeout(playNext, 600));
  player.play().catch(() => {});
}

function closePlayer() {
  const dlg = document.getElementById('player-dialog');
  if (dlg) dlg.close();
  if (player) { try { player.pause(); } catch(e) {} }
}

document.getElementById('player-dialog')?.addEventListener('close', () => {
  if (player) { try { player.pause(); } catch(e) {} }
});

/* ── Audio player ── */
function openAudio(streamUrl, title) {
  const dlg = document.getElementById('audio-dialog');
  const titleEl = document.getElementById('audio-title');
  const audio = document.getElementById('audio-player');
  if (!dlg || !audio) return;
  if (titleEl) titleEl.textContent = title || '';
  audio.src = streamUrl;
  dlg.showModal();
  audio.play().catch(() => {});
}

function closeAudio() {
  const dlg = document.getElementById('audio-dialog');
  const audio = document.getElementById('audio-player');
  if (dlg) dlg.close();
  if (audio) audio.pause();
}

document.getElementById('audio-dialog')?.addEventListener('close', () => {
  document.getElementById('audio-player')?.pause();
});

/* ── Log dialog ── */
function openLog(jobId, title) {
  const dlg = document.getElementById('log-dialog');
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
    if (r.ok) { showToast('Переименовано'); const mi=document.getElementById('media-inner'); if(mi) htmx.trigger(mi,'mediaRefresh'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

/* ── Delete file ── */
function deleteFile(itemId) {
  fetch(base() + 'items/' + itemId, { method: 'DELETE' })
    .then(r => {
      if (r.ok) { showToast('Файл удалён'); const mi=document.getElementById('media-inner'); if(mi) htmx.trigger(mi,'mediaRefresh'); }
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
    if (r.ok) { const mi=document.getElementById('media-inner'); if(mi) htmx.trigger(mi,'mediaRefresh'); }
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
  const bar = document.getElementById('action-bar');
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
    if (r.ok) { clearSelection(); const mi=document.getElementById('media-inner'); if(mi) htmx.trigger(mi,'mediaRefresh'); }
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
    if (r.ok) { clearSelection(); const mi=document.getElementById('media-inner'); if(mi) htmx.trigger(mi,'mediaRefresh'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
}

/* ── Collection dropdown (bulk) ── */
let collDropOpen = false;

function toggleCollDropdown() {
  const dd = document.getElementById('coll-dropdown');
  if (!dd) return;
  collDropOpen = !collDropOpen;
  dd.classList.toggle('hidden', !collDropOpen);
  if (collDropOpen) {
    dd.innerHTML = '<div style="padding:.5rem;color:var(--text-2);font-size:.8rem">Загрузка…</div>';
    fetch(base() + 'collections')
      .then(r => r.json())
      .then(cols => {
        if (!cols.length) {
          dd.innerHTML = '<div style="padding:.5rem;color:var(--text-2);font-size:.8rem">Нет коллекций</div>';
          return;
        }
        dd.innerHTML = cols.map(c =>
          `<button class="row-menu-item" onclick="addToCollection('${c.ID}')">${c.Name}</button>`
        ).join('');
      })
      .catch(() => { dd.innerHTML = '<div style="padding:.5rem;color:var(--danger)">Ошибка</div>'; });
  }
}

function addToCollection(collId) {
  if (!selectedJobs.size) return;
  fetch(base() + 'collections/' + collId + '/jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ job_ids: [...selectedJobs] })
  }).then(r => {
    if (r.ok) { clearSelection(); const mi=document.getElementById('media-inner'); if(mi) htmx.trigger(mi,'mediaRefresh'); showToast('Добавлено в коллекцию'); }
    else r.text().then(t => showToast('Ошибка: ' + t));
  });
  const dd = document.getElementById('coll-dropdown');
  if (dd) dd.classList.add('hidden');
  collDropOpen = false;
}

/* ── Status filter (legacy compat + status chip clicks) ── */
function activateStatusFilter(status) {
  filter.tag = status;
  filter.kind = '';
  const ft = document.getElementById('filter-tag');
  if (ft) ft.value = status;
  applyFilter();
}

/* ── Dialogs: close on backdrop click ── */
document.addEventListener('click', (e) => {
  if (e.target.tagName === 'DIALOG') e.target.close();
});

/* ── Collection management (sidebar refresh after create/delete) ── */
document.body.addEventListener('collectionsRefresh', () => {
  // Reload sidebar by refreshing the page shell (simplest approach)
  // In future, could fetch /library/sidebar fragment
  const sidebar = document.getElementById('sidebar');
  if (sidebar) htmx.ajax('GET', 'library/sidebar', { target: '#sidebar', swap: 'outerHTML' });
});
