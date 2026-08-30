// Точка входа интерфейса: собирает модули и подписывается на события сервера.

import { initActions, auditActions, register } from './actions.js';
import { initToast } from './toast.js';
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

function initSSE() {
  const base = document.querySelector('base')?.href || '/';
  const es = new EventSource(`${base}events`);
  for (const [topic, event] of Object.entries(SSE_TOPICS)) {
    es.addEventListener(topic, () => htmx.trigger(document.body, event));
  }
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
initSSE();

// Разметка и обработчики живут порознь, поэтому сверяем их при загрузке и после
// каждой подмены фрагмента: несуществующее действие попадёт в консоль, а не
// превратится в молча неработающую кнопку.
auditActions();
document.body.addEventListener('htmx:afterSwap', (e) => auditActions(e.target));
