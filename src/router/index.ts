import { createRouter, createWebHistory } from 'vue-router';
import DashboardView from '@/views/DashboardView.vue';
import SpaceHomeView from '@/views/SpaceHomeView.vue';
import SpaceDiscussView from '@/views/SpaceDiscussView.vue';
import SpaceProjectView from '@/views/SpaceProjectView.vue';
import ProjectFileView from '@/views/ProjectFileView.vue';
import AboutView from '@/views/AboutView.vue';
import HelpView from '@/views/HelpView.vue';

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      name: 'dashboard',
      component: DashboardView,
    },
    {
      path: '/join/:space/',
      name: 'join',
      component: DashboardView,
    },
    {
      path: '/:space/',
      name: 'space-home',
      component: SpaceHomeView,
    },
    {
      path: '/:space/project/:project',
      name: 'project',
      component: SpaceProjectView,
    },
    {
      path: '/:space/project/:project/:filename(.+)+',
      name: 'project-file',
      component: ProjectFileView,
    },
    {
      path: '/:space/discuss/:channel',
      name: 'discuss',
      component: SpaceDiscussView,
    },
    {
      path: '/about',
      name: 'about',
      component: AboutView,
    },
    {
      path: '/help',
      name: 'help',
      component: HelpView,
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/',
    },
  ],
});

export default router;
