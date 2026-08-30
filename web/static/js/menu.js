// Выпадающие меню строк и поведение диалогов.

import { register } from './actions.js';
import { closeDropdown, isDropdownOpen } from './collections.js';

let openMenu = null;

function closeMenu() {
  openMenu?.classList.remove('open');
  openMenu = null;
}

function toggleMenu(button, event) {
  // Меню открывается кликом, а глобальный обработчик ниже закрывает всё
  // открытое — без остановки всплытия оно закрылось бы тут же.
  event.stopPropagation();

  const menu = button.closest('.row-menu-wrap')?.querySelector('.row-menu');
  if (!menu) return;
  if (openMenu && openMenu !== menu) closeMenu();

  menu.classList.toggle('open');
  openMenu = menu.classList.contains('open') ? menu : null;
}

export function initMenus() {
  register({
    'row-menu': toggleMenu,
    'dialog-close': (el) => document.getElementById(el.dataset.dialog)?.close(),
  });

  // Любой клик мимо закрывает меню и выпадающий список коллекций.
  document.addEventListener('click', (e) => {
    if (openMenu) closeMenu();
    if (isDropdownOpen() && !e.target.closest('.coll-dropdown-wrap')) closeDropdown();
  });

  // Клик по подложке диалога закрывает его; окно плеера вместо этого сворачивается
  // (его обработчик close живёт в player.js).
  document.addEventListener('click', (e) => {
    if (e.target.tagName === 'DIALOG' && e.target.id !== 'player-dialog') {
      e.target.close();
    }
  });
}
