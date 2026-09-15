// Точка входа интерфейса: собирает модули и подписывается на события сервера.

import { initActions, auditActions, register } from './actions.js';
import { initToast, showToast } from './toast.js';
import { initFilters } from './filters.js';
import { initPlayer } from './player.js';
import { initSelection } from './selection.js';
import { initCollections } from './collections.js';
import { initLibrary } from './library.js';
import { initMenus } from './menu.js';

// Сервер присылает конкретные темы — обновляем только затронутую часть страницы.
const SSE_TOPICS = {
  library: 'mediaRefresh',
  queue: 'queueRefresh',
  tags: 'tagsRefresh',
  collections: 'collectionsRefresh',
};

function refreshAll() {
  for (const event of Object.values(SSE_TOPICS)) htmx.trigger(document.body, event);
}

// Встроенное переподключение EventSource спасает только от обрыва сети. Если
// сервер ответил кодом, отличным от успешного — например, 401 после истечения
// cookie или перезапуска с новым WEB_TOKEN, — соединение закрывается
// окончательно. Страница продолжала выглядеть живой, но список, очередь и
// прогресс не обновлялись уже никогда: пользователь видел вечное
// «скачивается» и решал, что приложение зависло.
function initSSE() {
  const base = document.querySelector('base')?.href || '/';
  let retry = 0;
  let everConnected = false;
  let warned = false;

  const connect = () => {
    const es = new EventSource(`${base}events`);

    es.onopen = () => {
      retry = 0;
      if (everConnected) {
        // Пока связи не было, данные могли измениться — перечитываем всё.
        refreshAll();
        if (warned) {
          showToast('Связь с сервером восстановлена');
          warned = false;
        }
      }
      everConnected = true;
    };

    for (const [topic, event] of Object.entries(SSE_TOPICS)) {
      es.addEventListener(topic, () => htmx.trigger(document.body, event));
    }

    es.onerror = () => {
      // Пока соединение не закрыто, EventSource переподключится сам.
      if (es.readyState !== EventSource.CLOSED) return;
      es.close();
      retry += 1;
      if (retry >= 3 && !warned) {
        showToast('Связь с сервером потеряна. Обновите страницу, если она не вернётся', true);
        warned = true;
      }
      setTimeout(connect, Math.min(30000, 1000 * 2 ** (retry - 1)));
    };
  };

  connect();
}

// Почти все действия строки и очереди — это hx-post/hx-delete с
// hx-swap="none". HTMX при 4xx/5xx ничего не подменяет и молчит, поэтому
// отказ сервера был полностью невидим: пользователь жал «Удалить навсегда» у
// невидимого задания, не получал ни изменения строки, ни сообщения — и жал
// ещё несколько раз. Пояснение сервер присылал телом ответа, но его никто не
// показывал.
function initRequestErrors() {
  document.body.addEventListener('htmx:responseError', (e) => {
    const xhr = e.detail?.xhr;
    const text = (xhr?.responseText || '').trim();
    showToast(text || `Ошибка ${xhr?.status ?? ''}`.trim(), true);
  });
  document.body.addEventListener('htmx:sendError', () => {
    showToast('Сервер недоступен: проверьте соединение', true);
  });
}

// «noop» помечает элементы внутри кликабельной строки, которые сами ничего не
// делают, но и открывать плеер не должны: чип статуса, крестик снятия тега.
// Поиск обработчика останавливается на них.
register({ noop: () => {} });

initToast();
initFilters();
initPlayer();
initSelection();
initCollections();
initLibrary();
initMenus();
initActions();
initRequestErrors();
initSSE();

// Разметка и обработчики живут порознь, поэтому сверяем их при загрузке и после
// каждой подмены фрагмента: несуществующее действие попадёт в консоль, а не
// превратится в молча неработающую кнопку.
auditActions();
document.body.addEventListener('htmx:afterSwap', (e) => auditActions(e.target));
