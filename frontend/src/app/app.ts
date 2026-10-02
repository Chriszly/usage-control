import { Component } from '@angular/core';

import { Dashboard } from './dashboard/dashboard';
import { LanguageSwitcher } from './language/language-switcher';

@Component({
  selector: 'app-root',
  imports: [Dashboard, LanguageSwitcher],
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {}
