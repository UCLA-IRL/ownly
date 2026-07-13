<template>
  <aside class="menu main-nav has-background-primary soft-if-dark">
    <div class="top-sheet">
      <div class="logo-row">
        <img alt="logo" class="logo" src="@/assets/logo-white.svg" />
        <label v-if="showTheme" class="theme-switch">
          <input type="checkbox" />
          <span class="track">
            <FontAwesomeIcon class="track-icon sun" :icon="faSun" />
            <FontAwesomeIcon class="track-icon moon" :icon="faMoon" />
            <span class="knob" />
          </span>
        </label>
      </div>
      <p class="menu-label">General</p>
      <ul class="menu-list">
        <li><a :class="{ 'is-active': activeItem === 'dashboard' }">
          <FontAwesomeIcon class="mr-1" :icon="faTableCells" size="sm" />Dashboard
        </a></li>
      </ul>
      <p class="menu-label">About</p>
      <ul class="menu-list">
        <li><a><FontAwesomeIcon class="mr-1" :icon="faCircleInfo" size="sm" />About</a></li>
        <li><a><FontAwesomeIcon class="mr-1" :icon="faLightbulb" size="sm" />Getting Started</a></li>
        <li><a><FontAwesomeIcon class="mr-1" :icon="faGithub" size="sm" />GitHub</a></li>
      </ul>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faTableCells, faCircleInfo, faLightbulb, faSun, faMoon } from '@fortawesome/free-solid-svg-icons';
import { faGithub } from '@fortawesome/free-brands-svg-icons';

defineProps({
  showTheme: {
    type: Boolean,
    default: false,
  },
  activeItem: {
    type: String,
    default: 'dashboard',
  },
});
</script>

<style scoped lang="scss">
.main-nav {
  padding: 8px;
  border-radius: 6px;
  width: 230px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);

  .top-sheet {
    display: flex;
    flex-direction: column;
  }

  .logo-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin: 0 5px 10px;
  }

  .logo {
    display: block;
    height: 30px;
    margin: 3px 0;
  }

  .theme-switch {
    position: relative;
    display: inline-flex;
    align-items: center;
    cursor: pointer;

    input {
      position: absolute;
      opacity: 0;
      width: 0;
      height: 0;
    }

    .track {
      position: relative;
      width: 40px;
      height: 20px;
      border-radius: 10px;
      background: rgba(255, 255, 255, 0.1);
      transition: background 0.25s ease;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 5px;
      box-sizing: border-box;
    }

    .track-icon {
      font-size: 10px;
      color: rgba(255, 255, 255, 0.35);
      transition: color 0.25s ease;
      z-index: 1;
    }

    .knob {
      position: absolute;
      top: 2px;
      left: 2px;
      width: 16px;
      height: 16px;
      border-radius: 50%;
      background: rgba(255, 255, 255, 0.5);
      transition: left 0.25s ease, background 0.25s ease;
    }

    input:checked ~ .track .knob {
      left: 22px;
    }

    &:hover .track {
      background: rgba(255, 255, 255, 0.18);
    }

    &:hover .track-icon {
      color: rgba(255, 255, 255, 0.7);
    }

    &:hover .knob {
      background: rgba(255, 255, 255, 0.8);
    }
  }

  .menu-label {
    color: #bbb;
    font-size: 0.6rem;
    margin: 6px 8px 2px;
  }

  .menu-list {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  :deep(li > a) {
    background-color: transparent;
    color: white;
    display: flex;
    align-items: center;
    min-height: 26px;
    font-size: 0.85rem;

    &:hover {
      background-color: rgba(255, 255, 255, 0.1);
    }

    &.is-active,
    &.router-link-active {
      background: rgba(255, 255, 255, 0.08);
      color: white;
      position: relative;

      &::before {
        content: '';
        position: absolute;
        left: 0;
        top: 0;
        bottom: 0;
        width: 3px;
        border-radius: 6px 0 0 6px;
        background: var(--sidebar-highlight-bg);
        pointer-events: none;
      }
    }
  }
}
</style>
