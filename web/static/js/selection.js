// Массовое выделение строк и диалог ID3-тегов.

import { register, registerChange } from './actions.js';
import { api, jsonBody, fail } from './api.js';
import { showToast } from './toast.js';

// Выделение хранится по строкам, а не по заданиям: у одного задания бывает
// несколько файлов (видео и извлечённое из него аудио, файлы плейлиста), и
// ключ по job_id затирал бы их друг другом — терялся и счётчик, и сам файл
// в операциях над тегами и извлечением.
const selection = new Map(); // ключ строки → сведения о ней

// rowKey — файл, а для строки без файла (в очереди, упавшей) — само задание.
function rowKey(jobId, itemId) {
  return itemId || `job:${jobId}`;
}

export function selectedJobIDs() {
  return [...new Set([...selection.values()].map((e) => e.jobId))];
}

export function hasSelection() {
  return selection.size > 0;
}

// meta собирает данные строки: задание, файл, тип и текущие ID3-теги.
function meta(row, jobId) {
  return {
    jobId,
    itemId: row.dataset.itemId || '',
    kind: row.dataset.kind || '',
    title: row.dataset.metaTitle || '',
    artist: row.dataset.metaArtist || '',
    album: row.dataset.metaAlbum || '',
    year: row.dataset.metaYear || '',
    genre: row.dataset.metaGenre || '',
  };
}

function select(row, jobId) {
  const entry = meta(row, jobId);
  selection.set(rowKey(jobId, entry.itemId), entry);
}

function updateActionBar() {
  const bar = document.getElementById('action-bar');
  if (!bar) return;

  const n = selection.size;
  const count = document.getElementById('select-count');
  if (count) count.textContent = `${n} выбрано`;
  bar.classList.toggle('hidden', n === 0);

  // Кнопка ID3-тегов имеет смысл, только если выделено одно лишь аудио.
  const metaBtn = document.getElementById('action-meta-btn');
  if (metaBtn) {
    const allAudio = n > 0 && [...selection.values()].every((e) => e.kind === 'audio' && e.itemId);
    metaBtn.style.display = allAudio ? '' : 'none';
  }

  // Извлекать дорожку есть из чего, только если в выделении попались видео.
  const extractBtn = document.getElementById('action-extract-btn');
  if (extractBtn) {
    extractBtn.style.display = selectedVideoIDs().length > 0 ? '' : 'none';
  }
}

// selectedVideoIDs — идентификаторы выделенных видеофайлов.
function selectedVideoIDs() {
  return [...selection.values()]
    .filter((e) => e.kind === 'video' && e.itemId)
    .map((e) => e.itemId);
}

export function clearSelection() {
  selection.clear();
  document.querySelectorAll('.row-checkbox:checked').forEach((cb) => { cb.checked = false; });
  updateActionBar();
}

function onRowSelect(checkbox) {
  const row = checkbox.closest('.media-row');
  if (!row) return;
  const jobId = checkbox.value;

  if (checkbox.checked) select(row, jobId);
  else selection.delete(rowKey(jobId, row.dataset.itemId || ''));
  updateActionBar();
}

function selectAllVisible() {
  document.querySelectorAll('#media-inner .row-checkbox:not(:checked)').forEach((cb) => {
    cb.checked = true;
    const row = cb.closest('.media-row');
    if (row) select(row, cb.value);
  });
  updateActionBar();
}

// ── Диалог ID3-тегов ─────────────────────────────────────────────────────────

const META_FIELDS = ['title', 'artist', 'album', 'year', 'genre'];

// singleTarget — файл, открытый из меню строки (а не из массового выделения).
let singleTarget = null;

function populate(count, entry) {
  const title = document.getElementById('meta-dialog-title');
  if (title) title.textContent = count === 1 ? 'Теги аудио' : `Теги аудио (${count} файлов)`;

  const note = document.getElementById('meta-count-note');
  if (note) note.textContent = count > 1 ? `Будет применено к ${count} файлам` : '';

  META_FIELDS.forEach((f) => {
    const input = document.getElementById(`meta-${f}`);
    const row = input?.closest('.meta-row');
    const check = row?.querySelector('.meta-check');
    if (input) input.value = entry ? (entry[f] || '') : '';
    if (check) check.checked = count === 1;
    row?.classList.toggle('dimmed', count !== 1);
  });
}

function openFromRow(button) {
  const row = button.closest('.media-row');
  if (!row) return;
  singleTarget = meta(row, row.dataset.jobId || '');
  populate(1, singleTarget);
  document.getElementById('meta-dialog')?.showModal();
}

function openForSelection() {
  singleTarget = null;
  const entries = [...selection.values()];
  populate(entries.length, entries.length === 1 ? entries[0] : null);
  document.getElementById('meta-dialog')?.showModal();
}

function onCheckChange(check) {
  const row = check.closest('.meta-row');
  if (row) row.classList.toggle('dimmed', !check.checked);
  const input = row?.querySelector('.meta-input');
  if (input && check.checked) input.focus();
}

async function applyMeta() {
  const dlg = document.getElementById('meta-dialog');

  // Применяются только отмеченные поля: пустое поле без галочки не должно
  // затирать существующий тег.
  const fields = {};
  document.querySelectorAll('#meta-dialog .meta-row').forEach((row) => {
    const check = row.querySelector('.meta-check');
    const input = row.querySelector('.meta-input');
    if (check?.checked && input) fields[row.dataset.field] = input.value;
  });

  const itemIds = singleTarget
    ? [singleTarget.itemId]
    : [...selection.values()].map((e) => e.itemId).filter(Boolean);

  if (!Object.keys(fields).length || !itemIds.length) {
    dlg?.close();
    singleTarget = null;
    return;
  }

  try {
    await api('items/meta-bulk', jsonBody('POST', { item_ids: itemIds, fields }));
    clearSelection();
    showToast('Теги обновлены');
  } catch (e) {
    fail(e);
  } finally {
    dlg?.close();
    singleTarget = null;
  }
}

// ── Массовые действия ────────────────────────────────────────────────────────

async function bulkTag() {
  const name = window.prompt('Тег для всех выбранных:');
  if (!name || !hasSelection()) return;
  try {
    await api('media/bulk-tag', jsonBody('POST', { tag: name, job_ids: selectedJobIDs() }));
    clearSelection();
  } catch (e) {
    fail(e);
  }
}

async function bulkHide() {
  if (!hasSelection()) return;
  try {
    await api('media/bulk-hide', jsonBody('POST', { job_ids: selectedJobIDs() }));
    clearSelection();
  } catch (e) {
    fail(e);
  }
}

// bulkExtractAudio ставит извлечение дорожек из всех выделенных видео.
async function bulkExtractAudio() {
  const itemIds = selectedVideoIDs();
  if (!itemIds.length) return;
  try {
    await api('items/extract-audio-bulk', jsonBody('POST', { item_ids: itemIds }));
    clearSelection();
  } catch (e) {
    fail(e);
  }
}

export function initSelection() {
  register({
    'select-all': selectAllVisible,
    'selection-clear': clearSelection,
    'bulk-tag': bulkTag,
    'bulk-hide': bulkHide,
    'bulk-extract-audio': bulkExtractAudio,
    'meta-open-selection': openForSelection,
    'meta-open-row': openFromRow,
    'meta-apply': applyMeta,
  });

  registerChange({
    'row-select': onRowSelect,
    'meta-check': onCheckChange,
  });
}
