// Фильтр медиатеки: раздел (медиатека/очередь), тип файла, коллекция, поиск, теги.
//
// Состояние живёт здесь и зеркалится в скрытую форму #filter-form, которую HTMX
// подмешивает в каждый запрос фрагментов. Фильтрация серверная — клиент строк
// не прячет.

import { register, registerInput } from './actions.js';

const filter = { q: '', kind: '', tag: '' };

// current отдаёт копию: снаружи состояние менять нельзя.
export function current() {
  return { ...filter };
}

function syncForm() {
  const set = (id, value) => {
    const el = document.getElementById(id);
    if (el) el.value = value;
  };
  set('filter-q', filter.q);
  set('filter-kind', filter.kind);
  set('filter-tag', filter.tag);
}

function refreshLibrary() {
  const inner = document.getElementById('media-inner');
  if (inner) htmx.trigger(inner, 'mediaRefresh');
}

export function applyFilter() {
  syncForm();
  refreshLibrary();
  // Счётчики тегов зависят от фильтра — обновляем облако вместе со списком.
  htmx.trigger(document.body, 'tagsRefresh');
}

// ── Разделы ──────────────────────────────────────────────────────────────────

function showSection(name) {
  const lib = document.getElementById('lib-section');
  const queue = document.getElementById('queue-section');
  if (lib) lib.style.display = name === 'library' ? '' : 'none';
  if (queue) queue.style.display = name === 'queue' ? '' : 'none';
  document.getElementById('sidebar-queue-btn')?.classList.toggle('active', name === 'queue');
}

function clearSidebarActive(selector) {
  document.querySelectorAll(selector).forEach((b) => b.classList.remove('active'));
}

// ── Полоса «Воспроизвести всё» ───────────────────────────────────────────────

export function showPlayAll(name) {
  const bar = document.getElementById('play-all-bar');
  if (!bar) return;
  const title = document.getElementById('play-all-title');
  if (title) title.textContent = name;
  bar.classList.add('visible');
}

export function hidePlayAll() {
  document.getElementById('play-all-bar')?.classList.remove('visible');
}

// ── Действия ─────────────────────────────────────────────────────────────────

function openQueue() {
  filter.q = '';
  filter.kind = '';
  filter.tag = '';
  hidePlayAll();
  clearSidebarActive('.sidebar-nav-item[data-kind]');
  clearSidebarActive('.sidebar-nav-item[data-coll]');
  showSection('queue');
}

function setKind(el) {
  const kind = el.dataset.kind || '';
  filter.kind = kind;
  filter.tag = '';
  clearSidebarActive('.sidebar-nav-item[data-coll]');
  document.querySelectorAll('.sidebar-nav-item[data-kind]').forEach((b) => {
    b.classList.toggle('active', b.dataset.kind === kind);
  });
  showSection('library');
  hidePlayAll();
  applyFilter();
}

function setCollection(el) {
  const name = el.dataset.coll || '';
  filter.tag = name;
  filter.kind = '';
  clearSidebarActive('.sidebar-nav-item[data-kind]');
  document.querySelectorAll('.sidebar-nav-item[data-coll]').forEach((b) => {
    b.classList.toggle('active', b.dataset.coll === name);
  });
  showSection('library');
  showPlayAll(name);
  applyFilter();
}

function toggleTag(el) {
  const tag = el.dataset.tag || '';

  if (filter.tag === tag) {
    filter.tag = '';
    el.classList.remove('active');
    clearSidebarActive('.sidebar-nav-item[data-coll]');
    document.querySelector('.sidebar-nav-item[data-kind=""]')?.classList.add('active');
    hidePlayAll();
  } else {
    filter.tag = tag;
    document.querySelectorAll('#tag-cloud .chip').forEach((b) => b.classList.remove('active'));
    el.classList.add('active');

    if (el.classList.contains('coll')) {
      showPlayAll(tag);
      const collBtn = document.querySelector(`.sidebar-nav-item[data-coll="${CSS.escape(tag)}"]`);
      if (collBtn) {
        clearSidebarActive('.sidebar-nav-item');
        collBtn.classList.add('active');
      }
    } else {
      hidePlayAll();
    }
  }

  syncForm();
  refreshLibrary();
}

// filterByTag вызывается кликом по тегу в строке медиатеки.
// Если такой тег есть в облаке — жмём его чип, чтобы подсветка не разъехалась.
function filterByTag(el) {
  const tag = el.dataset.tag || '';
  const chip = document.querySelector(`#tag-cloud .chip[data-tag="${CSS.escape(tag)}"]`);
  if (chip) {
    if (filter.tag !== tag) chip.click();
    return;
  }
  filter.tag = filter.tag === tag ? '' : tag;
  applyFilter();
}

// clearTagIfActive сбрасывает фильтр, если удалили активную коллекцию.
export function clearTagIfActive(tag) {
  if (filter.tag !== tag) return;
  filter.tag = '';
  hidePlayAll();
  applyFilter();
}

export function initFilters() {
  register({
    'nav-queue': openQueue,
    'nav-kind': setKind,
    'nav-coll': setCollection,
    'toggle-tag': toggleTag,
    'filter-tag': filterByTag,
    'expand-tags': () => document.getElementById('tag-cloud')?.classList.add('tag-cloud-expanded'),
  });

  registerInput({
    search: (el) => {
      filter.q = el.value;
      applyFilter();
    },
  });
}
