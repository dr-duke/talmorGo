// Коллекции: выпадающий список в панели действий и управление из сайдбара.

import { register } from './actions.js';
import { api, jsonBody, escapeHtml, fail } from './api.js';
import { showToast } from './toast.js';
import { selectedJobIDs, hasSelection, clearSelection } from './selection.js';
import { clearTagIfActive } from './filters.js';

let dropdownOpen = false;

export function closeDropdown() {
  document.getElementById('coll-dropdown')?.classList.add('hidden');
  dropdownOpen = false;
}

export function isDropdownOpen() {
  return dropdownOpen;
}

function render(cols) {
  const dd = document.getElementById('coll-dropdown');
  if (!dd) return;

  // Имена задаёт пользователь — экранируем, а действие вешаем через data-action.
  const items = cols.map((c) =>
    `<button class="row-menu-item" data-action="coll-add" data-coll-id="${escapeHtml(c.id)}">${escapeHtml(c.name)}</button>`
  ).join('');

  dd.innerHTML = `${items}
    <div class="row-menu-divider"></div>
    <button class="row-menu-item" data-action="coll-create">
      <span class="mi">create_new_folder</span>Создать коллекцию…
    </button>`;
}

async function toggleDropdown() {
  const dd = document.getElementById('coll-dropdown');
  if (!dd) return;

  dropdownOpen = !dropdownOpen;
  dd.classList.toggle('hidden', !dropdownOpen);
  if (!dropdownOpen) return;

  dd.innerHTML = '<div class="coll-dropdown-note">Загрузка…</div>';
  try {
    const resp = await api('collections');
    render(await resp.json());
  } catch {
    dd.innerHTML = '<div class="coll-dropdown-note error">Ошибка</div>';
  }
}

async function addToCollection(collId) {
  if (!hasSelection()) return;
  closeDropdown();
  try {
    await api(`collections/${collId}/jobs`, jsonBody('POST', { job_ids: selectedJobIDs() }));
    clearSelection();
    showToast('Добавлено в коллекцию');
  } catch (e) {
    fail(e);
  }
}

async function createAndAdd() {
  closeDropdown();
  const name = window.prompt('Название новой коллекции:');
  if (!name) return;
  try {
    const resp = await api('collections', jsonBody('POST', { name }));
    const col = await resp.json();
    await addToCollection(col.id);
  } catch {
    showToast('Ошибка создания коллекции');
  }
}

async function renameCollection(el) {
  const { collId, collName } = el.dataset;
  const name = window.prompt('Новое название коллекции:', collName);
  if (!name || name === collName) return;
  try {
    await api(`collections/${collId}`, jsonBody('PATCH', { name }));
    showToast('Коллекция переименована');
  } catch (e) {
    fail(e);
  }
}

async function deleteCollection(el) {
  const { collId, collName } = el.dataset;
  if (!window.confirm(`Удалить коллекцию «${collName}»? Видео останутся в медиатеке.`)) return;
  try {
    await api(`collections/${collId}`, { method: 'DELETE' });
    clearTagIfActive(collName); // коллекция могла быть активным фильтром
    showToast('Коллекция удалена');
  } catch (e) {
    fail(e);
  }
}

export function initCollections() {
  register({
    'coll-dropdown': toggleDropdown,
    'coll-add': (el) => addToCollection(el.dataset.collId),
    'coll-create': createAndAdd,
    'coll-rename': renameCollection,
    'coll-delete': deleteCollection,
  });

  // Сайдбар перерисовывается при изменении списка коллекций.
  document.body.addEventListener('collectionsRefresh', () => {
    if (document.getElementById('sidebar')) {
      htmx.ajax('GET', 'library/sidebar', { target: '#sidebar', swap: 'outerHTML' });
    }
  });
}
