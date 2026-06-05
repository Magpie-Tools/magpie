import { Routes } from '@angular/router';
import {DashboardComponent} from './dashboard/dashboard.component';
import {ProxiesComponent} from './proxies/proxies.component';
import {ProxyDetailComponent} from './proxies/proxy-detail/proxy-detail.component';
import {RegisterComponent} from './auth/register/register.component';
import {LoginComponent} from './auth/login/login.component';
import {ForgotPasswordComponent} from './auth/forgot-password/forgot-password.component';
import {ResetPasswordComponent} from './auth/reset-password/reset-password.component';
import {AuthGuardService} from './services/authorization/auth-guard.service';
import {AccountComponent} from './account/account.component';
import {AuthGuardAdminService} from './services/authorization/auth-guard-admin.service';
import {AuthLoginGuardService} from './services/authorization/auth-login-guard.service';
import {AddProxiesComponent} from './proxies/proxy-list/add-proxies/add-proxies.component';
import {CheckerJudgesComponent} from './checker/judges/checker-judges.component';
import {CheckerSettingsComponent} from './checker/settings/checker-settings.component';
import {RotatingProxiesComponent} from './rotating-proxies/rotating-proxies.component';
import {AdminCheckerComponent} from './admin/admin-checker/admin-checker.component';
import {AdminScraperComponent} from './admin/admin-scraper/admin-scraper.component';
import {AdminPluginsComponent} from './admin/admin-plugins/admin-plugins.component';
import {PluginGeoliteComponent} from './admin/admin-plugins/plugin-geolite/plugin-geolite.component';
import {PluginAbuseIPDBComponent} from './admin/admin-plugins/plugin-abuseipdb/plugin-abuseipdb.component';
import {AdminBlacklistComponent} from './admin/admin-blacklist/admin-blacklist.component';
import {ScraperComponent} from './scraper/scraper.component';
import {ScrapeSourceDetailComponent} from './scraper/scrape-source-detail/scrape-source-detail.component';
import {NotificationsComponent} from './notifications/notifications.component';

const authGuard = [AuthGuardService];
const adminGuard = [AuthGuardAdminService];
const loginGuard = [AuthLoginGuardService];

export const routes: Routes = [
  {path: 'register', component: RegisterComponent, canActivate: loginGuard},
  {path: 'login', component: LoginComponent, canActivate: loginGuard},
  {path: 'forgot-password', component: ForgotPasswordComponent},
  {path: 'reset-password', component: ResetPasswordComponent},

  {path: 'global/checker', component: AdminCheckerComponent, canActivate: adminGuard},
  {path: 'global/scraper', component: AdminScraperComponent, canActivate: adminGuard},
  {path: 'global/blacklist', component: AdminBlacklistComponent, canActivate: adminGuard},
  {path: 'plugins', component: AdminPluginsComponent, canActivate: adminGuard},
  {path: 'plugins/geolite', component: PluginGeoliteComponent, canActivate: adminGuard},
  {path: 'plugins/abuseipdb', component: PluginAbuseIPDBComponent, canActivate: adminGuard},
  {path: 'global/plugins', redirectTo: 'plugins', pathMatch: 'full'},
  {path: 'global/other', redirectTo: 'plugins', pathMatch: 'full'},

  {path: 'account', component: AccountComponent, canActivate: authGuard},
  {path: 'addProxies', component: AddProxiesComponent, canActivate: authGuard},
  {path: 'rotating', component: RotatingProxiesComponent, canActivate: authGuard},
  {path: 'proxies', component: ProxiesComponent, canActivate: authGuard},
  {path: 'proxies/:id', component: ProxyDetailComponent, canActivate: authGuard},
  {path: 'scraper/:id', component: ScrapeSourceDetailComponent, canActivate: authGuard},
  {path: 'scraper', component: ScraperComponent, canActivate: authGuard},
  {path: 'checker', redirectTo: 'checker/settings', pathMatch: 'full'},
  {path: 'checker/settings', component: CheckerSettingsComponent, canActivate: authGuard},
  {path: 'checker/judges', component: CheckerJudgesComponent, canActivate: authGuard},
  {path: 'notifications', component: NotificationsComponent, canActivate: authGuard},
  {path: 'dashboard', component: DashboardComponent, canActivate: authGuard},
  {path: '', component: DashboardComponent, canActivate: authGuard, pathMatch: 'full'},
  {path: '**', redirectTo: ''},
];
