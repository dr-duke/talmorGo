// Плеер: одно управление на видео и аудио.
//
// Одновременно играет только один вид: при переключении предыдущий сносится.
// Видео живёт в <dialog> с Plyr, аудио — в скрытом <audio>; нижняя панель общая.

import { register } from './actions.js';
import { base } from './api.js';
import { current as currentFilter } from './filters.js';

let plyr = null;
let kind = null; // 'video' | 'audio' | null
let minimizing = false; // окно закрывает playerMinimize, а не пользователь

let playlist = [];
let playlistIndex = -1;

const el = (id) => document.getElementById(id);

// ── Очередь воспроизведения ──────────────────────────────────────────────────

// Плейлист всегда берём с сервера: в DOM лежит только текущая страница выдачи,
// и очередь по клику на строку обрывалась бы на её конце.
async function fetchPlaylist() {
  const f = currentFilter();
  const params = new URLSearchParams();
  if (f.q) params.set('q', f.q);
  if (f.kind) params.set('kind', f.kind);
  if (f.tag) params.set('tag', f.tag);

  try {
    const resp = await fetch(`${base()}library/playlist?${params}`);
    if (!resp.ok) return [];
    return await resp.json();
  } catch {
    return [];
  }
}

async function playAll() {
  playlist = await fetchPlaylist();
  playlistIndex = 0;
  if (playlist.length) open(playlist[0].stream, playlist[0].title, 'video');
}

function playNext() {
  if (playlistIndex < 0 || playlistIndex >= playlist.length - 1) return;
  playlistIndex += 1;
  const next = playlist[playlistIndex];
  open(next.stream, next.title, 'video');
}

// ── Открытие ─────────────────────────────────────────────────────────────────

function rowOf(target) {
  return target.closest('.media-row');
}

async function activateRow(target) {
  const row = rowOf(target);
  if (!row?.dataset.stream) return;

  const rowKind = row.dataset.kind || 'video';
  if (rowKind === 'video' && !playlist.length) {
    playlist = await fetchPlaylist();
  }
  open(row.dataset.stream, row.dataset.title, rowKind);
}

function open(stream, title, mediaKind) {
  if (kind !== null && kind !== mediaKind) teardown();
  kind = mediaKind;

  const barTitle = el('pb-title');
  if (barTitle) barTitle.textContent = title || '';
  const icon = el('pb-kind-icon');
  if (icon) icon.textContent = mediaKind === 'audio' ? 'audio_file' : 'movie';

  if (mediaKind === 'video') {
    const idx = playlist.findIndex((p) => p.stream === stream);
    playlistIndex = idx >= 0 ? idx : 0;
    openVideo(stream, title);
  } else {
    openAudio(stream);
  }
  barShow();
}

function openVideo(stream, title) {
  const dlg = el('player-dialog');
  if (!dlg) return;

  const titleEl = el('player-title');
  if (titleEl) titleEl.textContent = title || '';

  if (plyr) {
    try { plyr.destroy(); } catch { /* плеер уже разрушен */ }
    plyr = null;
  }

  // Ссылку на видео берём только после destroy(): Plyr возвращает в DOM свой
  // исходный элемент, и записанный до этого src ушёл бы в отсоединённый узел —
  // из-за чего следующий ролик очереди не включался.
  const video = el('main-player');
  if (!video) return;

  video.src = stream;
  // showModal бросает исключение, если окно уже открыто.
  if (!dlg.open) dlg.showModal();

  plyr = new Plyr(video, {
    autoplay: true,
    controls: ['play-large', 'play', 'progress', 'current-time', 'mute', 'volume', 'captions', 'fullscreen'],
    keyboard: { focused: true, global: false },
    // iOS не умеет Fullscreen API для произвольных элементов: там
    // разворачивает только нативный плеер, и включает его именно iosNative.
    fullscreen: { enabled: true, fallback: true, iosNative: true },
  });

  // play() зовём сразу, внутри жеста пользователя, иначе браузер откажет.
  plyr.play().catch(() => {});

  plyr.on('ended', () => setTimeout(playNext, 600));
  plyr.on('play', () => setPlayIcon(true));
  plyr.on('pause', () => setPlayIcon(false));
  plyr.on('timeupdate', updateVideoProgress);
}

function openAudio(stream) {
  const audio = el('audio-player');
  if (!audio) return;
  audio.src = stream;
  audio.play().catch(() => {});
}

// ── Остановка и сворачивание ─────────────────────────────────────────────────

function teardown() {
  if (kind === 'video') {
    const dlg = el('player-dialog');
    if (dlg?.open) {
      minimizing = true;
      dlg.close();
      minimizing = false;
    }
    if (plyr) {
      try { plyr.pause(); plyr.destroy(); } catch { /* уже разрушен */ }
      plyr = null;
    }
    const video = el('main-player');
    if (video) video.src = '';
  } else if (kind === 'audio') {
    const audio = el('audio-player');
    if (audio) { audio.pause(); audio.src = ''; }
  }
}

function playerClose() {
  // Сбрасываем kind до close(): событие close приходит асинхронно, и обработчик
  // отличает намеренное закрытие от клика по подложке именно по kind === null.
  const closingKind = kind;
  kind = null;
  playlist = [];
  playlistIndex = -1;

  if (closingKind === 'video') {
    const dlg = el('player-dialog');
    if (dlg?.open) dlg.close();
    if (plyr) {
      try { plyr.pause(); plyr.destroy(); } catch { /* уже разрушен */ }
      plyr = null;
    }
    const video = el('main-player');
    if (video) video.src = '';
  } else if (closingKind === 'audio') {
    const audio = el('audio-player');
    if (audio) { audio.pause(); audio.src = ''; }
  }
  barHide();
}

function playerMinimize() {
  const dlg = el('player-dialog');
  if (!dlg?.open) return;
  minimizing = true;
  dlg.close();
  minimizing = false;
  barShow();
}

function playerExpand() {
  if (kind !== 'video') return;
  const dlg = el('player-dialog');
  if (dlg && !dlg.open) dlg.showModal();
}

function playerToggle() {
  if (kind === 'video' && plyr) {
    if (plyr.paused) plyr.play(); else plyr.pause();
    return;
  }
  if (kind === 'audio') {
    const audio = el('audio-player');
    if (!audio) return;
    if (audio.paused) audio.play(); else audio.pause();
  }
}

// ── Нижняя панель ────────────────────────────────────────────────────────────

function barShow() {
  el('player-bar')?.classList.add('visible');
  document.body.classList.add('has-player');
  const expand = el('pb-expand-btn');
  if (expand) expand.style.display = kind === 'video' ? '' : 'none';
}

function barHide() {
  el('player-bar')?.classList.remove('visible');
  document.body.classList.remove('has-player');
  setProgress(0, 0);
  setPlayIcon(false);
}

function setPlayIcon(playing) {
  const icon = el('pb-play-icon');
  if (icon) icon.textContent = playing ? 'pause' : 'play_arrow';
}

function formatTime(s) {
  if (!isFinite(s) || s < 0) return '0:00';
  const m = Math.floor(s / 60);
  return `${m}:${String(Math.floor(s % 60)).padStart(2, '0')}`;
}

function setProgress(cur, dur) {
  const fill = el('pb-fill');
  const curEl = el('pb-current');
  const durEl = el('pb-duration');
  const pct = dur > 0 ? Math.min(100, (cur / dur) * 100) : 0;
  if (fill) fill.style.width = `${pct}%`;
  if (curEl) curEl.textContent = formatTime(cur);
  if (durEl) durEl.textContent = formatTime(dur);
}

function updateVideoProgress() {
  if (plyr) setProgress(plyr.currentTime, plyr.duration);
}

function seek(event) {
  const track = el('pb-track');
  if (!track) return;
  const rect = track.getBoundingClientRect();
  const pct = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width));

  if (kind === 'video' && plyr?.duration) {
    plyr.currentTime = plyr.duration * pct;
    return;
  }
  if (kind === 'audio') {
    const audio = el('audio-player');
    if (audio?.duration) audio.currentTime = audio.duration * pct;
  }
}

// ── Инициализация ────────────────────────────────────────────────────────────

function initDialog() {
  const dlg = el('player-dialog');
  if (!dlg) return;

  // Escape сворачивает в панель, а не останавливает воспроизведение.
  dlg.addEventListener('cancel', (e) => {
    e.preventDefault();
    playerMinimize();
  });

  dlg.addEventListener('close', () => {
    if (minimizing || kind === null) return;
    barShow(); // закрытие извне — тоже сворачивание
  });

  // Модальное окно само по клику на подложку не закрывается, поэтому
  // сворачиваем его вручную: воспроизведение продолжается в нижней панели.
  document.addEventListener('click', (e) => {
    if (e.target === dlg) playerMinimize();
  });
}

function initSeek() {
  const track = el('pb-track');
  if (!track) return;

  let dragging = false;
  track.addEventListener('mousedown', () => { dragging = true; });
  document.addEventListener('mousemove', (e) => { if (dragging) seek(e); });
  document.addEventListener('mouseup', () => { dragging = false; });
  track.addEventListener('touchmove', (e) => {
    e.preventDefault();
    seek(e.touches[0]);
  }, { passive: false });
}

function initAudioElement() {
  const audio = el('audio-player');
  if (!audio) return;

  const sync = () => { if (kind === 'audio') setProgress(audio.currentTime, audio.duration); };
  audio.addEventListener('timeupdate', sync);
  audio.addEventListener('durationchange', sync);
  audio.addEventListener('play', () => { if (kind === 'audio') setPlayIcon(true); });
  audio.addEventListener('pause', () => { if (kind === 'audio') setPlayIcon(false); });
  audio.addEventListener('ended', () => { if (kind === 'audio') setPlayIcon(false); });
}

export function initPlayer() {

  initDialog();
  initSeek();
  initAudioElement();

  register({
    'row-activate': (el_, e) => activateRow(e.target),
    'row-play': (el_, e) => activateRow(e.target),
    'play-all': playAll,
    'player-close': playerClose,
    'player-minimize': playerMinimize,
    'player-expand': playerExpand,
    'player-toggle': playerToggle,
    'player-seek': (el_, e) => seek(e),
  });
}
