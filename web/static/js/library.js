// Действия над строкой медиатеки: ссылки, переименование, удаление, теги, лог.

import { register } from './actions.js';
import { api, base, jsonBody, fail } from './api.js';
import { showToast } from './toast.js';

async function copyLink(el) {
  try {
    const resp = await api(`items/${el.dataset.itemId}/link`, { method: 'POST' });
    const { url } = await resp.json();
    await navigator.clipboard.writeText(url);
    showToast('Ссылка скопирована');
  } catch {
    showToast('Ошибка получения ссылки');
  }
}

async function revokeLink(el) {
  if (!window.confirm('Отозвать постоянную ссылку? Прежний адрес перестанет работать.')) return;
  try {
    await api(`items/${el.dataset.itemId}/link`, { method: 'DELETE' });
    showToast('Ссылка отозвана');
  } catch (e) {
    fail(e);
  }
}

async function copyURL(el) {
  await navigator.clipboard.writeText(el.dataset.url || '');
  showToast('URL скопирован');
}

async function renameFile(el) {
  const current = el.dataset.name || '';
  const name = window.prompt('Новое имя файла:', current);
  if (!name || name === current) return;
  try {
    await api(`items/${el.dataset.itemId}`, jsonBody('PATCH', { name }));
    showToast('Переименовано');
  } catch (e) {
    fail(e);
  }
}

async function deleteFile(el) {
  try {
    await api(`items/${el.dataset.itemId}`, { method: 'DELETE' });
    showToast('Файл удалён');
  } catch (e) {
    fail(e);
  }
}

async function addTag(el) {
  const name = window.prompt('Имя тега:');
  if (!name) return;
  try {
    await api(`jobs/${el.dataset.jobId}/tags`, jsonBody('POST', { name }));
  } catch (e) {
    fail(e);
  }
}

async function openLog(el) {
  const dlg = document.getElementById('log-dialog');
  if (!dlg) return;

  const { jobId, title } = el.dataset;
  const titleEl = document.getElementById('log-title');
  const content = document.getElementById('log-content');
  if (titleEl) titleEl.textContent = `Лог: ${title || jobId}`;
  if (content) content.textContent = 'Загрузка…';
  dlg.showModal();

  try {
    const resp = await fetch(`${base()}jobs/${jobId}/log`);
    const text = await resp.text();
    if (content) content.textContent = text || '(пусто)';
  } catch (e) {
    if (content) content.textContent = `Ошибка: ${e.message}`;
  }
}

export function initLibrary() {
  register({
    'copy-link': copyLink,
    'revoke-link': revokeLink,
    'copy-url': copyURL,
    'rename-file': renameFile,
    'delete-file': deleteFile,
    'add-tag': addTag,
    'open-log': openLog,
  });
}
