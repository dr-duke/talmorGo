// Делегирование событий.
//
// Разметка объявляет намерение (`data-action="row-play"`), а модули регистрируют
// обработчики. Раньше шаблоны вызывали глобальные функции прямо из onclick:
// переименование функции молча ломало кнопку, и именно так «умер» экран
// коллекций — шаблон ссылался на четыре несуществующие функции.
//
// Делегирование на document переживает подмену фрагментов HTMX: обработчики
// вешать заново не нужно.

const handlers = {
  click: new Map(),
  change: new Map(),
  input: new Map(),
};

// register регистрирует обработчики кликов: { 'row-play': (el, event) => {} }.
export function register(actions) {
  add('click', actions);
}

export function registerChange(actions) {
  add('change', actions);
}

export function registerInput(actions) {
  add('input', actions);
}

function add(type, actions) {
  for (const [name, fn] of Object.entries(actions)) {
    if (handlers[type].has(name)) {
      console.warn(`действие «${name}» уже зарегистрировано для ${type}`);
    }
    handlers[type].set(name, fn);
  }
}

const attr = {
  click: 'data-action',
  change: 'data-action-change',
  input: 'data-action-input',
};

export function initActions() {
  for (const type of Object.keys(handlers)) {
    document.addEventListener(type, (event) => {
      const el = event.target.closest(`[${attr[type]}]`);
      if (!el) return;
      const name = el.getAttribute(attr[type]);
      const fn = handlers[type].get(name);
      if (!fn) {
        console.warn(`нет обработчика для ${attr[type]}="${name}"`);
        return;
      }
      fn(el, event);
    });
  }
}

// known сообщает, зарегистрировано ли действие (используется в самопроверке).
export function known(type, name) {
  return handlers[type]?.has(name) ?? false;
}

// Проверка целостности: разметка не должна ссылаться на несуществующие действия.
// Ошибка видна сразу в консоли, а не превращается в молча неработающую кнопку.
export function auditActions(root = document) {
  for (const [type, a] of Object.entries(attr)) {
    root.querySelectorAll(`[${a}]`).forEach((el) => {
      const name = el.getAttribute(a);
      if (!handlers[type].has(name)) {
        console.warn(`разметка ссылается на неизвестное действие ${a}="${name}"`, el);
      }
    });
  }
}
