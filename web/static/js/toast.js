// Всплывающие уведомления.

let timer;

export function showToast(msg, isError = false) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.classList.toggle('toast-error', isError);
  el.classList.add('visible');
  clearTimeout(timer);
  // Ошибку держим дольше: её текст длиннее и его нужно успеть прочитать.
  timer = setTimeout(() => el.classList.remove('visible'), isError ? 6000 : 2800);
}

// Сервер может попросить показать уведомление заголовком HX-Trigger.
export function initToast() {
  document.body.addEventListener('showToast', (e) => showToast(e.detail?.value || ''));
}
