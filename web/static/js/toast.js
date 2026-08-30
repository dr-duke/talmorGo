// Всплывающие уведомления.

let timer;

export function showToast(msg) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.classList.add('visible');
  clearTimeout(timer);
  timer = setTimeout(() => el.classList.remove('visible'), 2800);
}

// Сервер может попросить показать уведомление заголовком HX-Trigger.
export function initToast() {
  document.body.addEventListener('showToast', (e) => showToast(e.detail?.value || ''));
}
