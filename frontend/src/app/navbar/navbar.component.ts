import {AfterViewInit, Component, ElementRef, OnInit} from '@angular/core';
import {NavigationEnd, Router, RouterLink, RouterLinkActive} from '@angular/router';
import {UserService} from "../services/authorization/user.service";
import {Button, ButtonDirective} from 'primeng/button';
import {Popover} from 'primeng/popover';
import {PanelMenu} from 'primeng/panelmenu';
import {MenuItem} from 'primeng/api';
import {Ripple} from 'primeng/ripple';
import {Badge} from 'primeng/badge';
import {ThemeService} from '../services/theme.service';

@Component({
  selector: 'app-navbar',
  imports: [
    RouterLink,
    RouterLinkActive,
    ButtonDirective,
    Popover,
    Button,
    PanelMenu,
    Ripple,
    Badge,
  ],
  templateUrl: './navbar.component.html',
  styleUrl: './navbar.component.scss'
})
export class NavbarComponent implements OnInit, AfterViewInit {
  menuItems: MenuItem[] = [];

  constructor(protected user: UserService,
              private router: Router,
              private elementRef: ElementRef,
              protected themeService: ThemeService,) {}

  ngOnInit() {
    this.updateMenuItems();

    setTimeout(() => this.updateMenuItems(), 1000); //Because of admin
  }

  updateMenuItems(): void {
    this.menuItems = [
      {
        label: 'Checker',
        icon: 'pi pi-wrench',
        styleClass: 'menu-title',
        hasExpandable: true,
        expanded: true,
        items: [
          {
            label: 'Settings',
            icon: 'pi pi-cog',
            routerLink: 'checker/settings',
          },
          {
            label: 'Judges',
            icon: 'pi pi-address-book',
            routerLink: 'checker/judges'
          }
        ]
      },
      {
        label: 'Admin',
        icon: 'pi pi-shield', // Add icon for the header
        styleClass: 'menu-title',
        hasExpandable: true,
        visible: UserService.isAdmin(),
        items: [
          {
            label: 'Global Checker',
            icon: 'pi pi-globe',
            routerLink: 'global/checker'
          },
          {
            label: 'Global Scraper',
            icon: 'pi pi-globe',
            routerLink: 'global/scraper'
          },
          {
            label: 'Global Blacklist',
            icon: 'pi pi-globe',
            routerLink: 'global/blacklist'
          },
          {
            label: 'Plugins',
            icon: 'pi pi-box',
            routerLink: '/plugins'
          }
        ]
      }
    ];
  }

  ngAfterViewInit() {
    //FIXES FIREFOX SELECTION BUG
    this.router.events.subscribe(event => {
      if (event instanceof NavigationEnd) {
        setTimeout(() => {
          this.removeFocusFromPanelMenu();
        }, 100);
      }
    });

    // Listen to panel menu clicks to handle toggle events
    const panelMenuElement = this.elementRef.nativeElement.querySelector('p-panelmenu');
    if (panelMenuElement) {
      panelMenuElement.addEventListener('click', (event: any) => {
        // Check if a header was clicked (toggle action)
        if (event.target.closest('.p-panelmenu-header')) {
          setTimeout(() => {
            this.removeFocusFromPanelMenu();
          }, 150); // Slightly longer delay for panel animation
        }
      });
    }
  }

  private removeFocusFromPanelMenu(): void {
    const focusedElements = this.elementRef.nativeElement.querySelectorAll('.p-focus');
    focusedElements.forEach((element: HTMLElement) => {
      element.classList.remove('p-focus');
      element.blur();
    });
  }

  protected getSrcPath() {
    let start = "../../assets/logo/magpie-light-"
    if (this.themeService.theme() === 'purple') {
      return start + "purple.svg"
    } else if (this.themeService.theme() === 'blue') {
      return start + "blue.svg"
    } else if (this.themeService.theme() === 'red') {
      return start + "red.svg"
    }
    return start + 'green.svg'
  }
}
