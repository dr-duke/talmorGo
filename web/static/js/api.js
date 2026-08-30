// Единая точка сетевых запросов.
//
// Ответ может нести заголовок HX-Trigger; тогда события (mediaRefresh,
// queueRefresh, tagsRefresh…) рассылаются так же, как для запросов самого HTMX.
// Без этого запросы из JS обновляли бы только часть интерфейса.

import { showToast } from './toast.js';

export function base() {
  return document.querySelector('base')?.href || '/';
}

export async function api(path, opts = {}) {
  const resp = await fetch(base() + path, opts);

  const trigger = resp.headers.get('HX-Trigger');
  if (trigger) {
    let events;
    try {
      events = JSON.parse(trigger);
    } catch {
      events = { [trigger]: true };
    }
    for (const [name, detail] of Object.entries(events)) {
      if (name === 'showToast') showToast(String(detail));
      else htmx.trigger(document.body, name);
    }
  }

  if (!resp.ok) {
    const text = await resp.text().catch(() => '');
    throw new Error(text || resp.statusText);
  }
  return resp;
}

// Тело JSON-запроса с нужными заголовками.
export function jsonBody(method, payload) {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  };
}

// Экранирование пользовательского текста перед вставкой в innerHTML.
export function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (ch) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]
  ));
}

// fail показывает ошибку запроса единообразно.
export function fail(err, prefix = 'Ошибка') {
  showToast(`${prefix}: ${err.message}`);
}
