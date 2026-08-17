// @ts-check

// Ported 1:1 from the old mkdocs.yml `nav:` (same grouping and labels).

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docsSidebar: [
    {type: 'doc', id: 'index', label: 'Home'},
    {type: 'doc', id: 'getting-started', label: 'Getting started'},
    {type: 'doc', id: 'coverage', label: 'Tracker coverage'},
    {type: 'doc', id: 'test-status', label: 'Test status'},
    {
      type: 'category',
      label: 'Guides',
      items: [
        {type: 'doc', id: 'guides/add-indexer', label: 'Adding an indexer'},
        {type: 'doc', id: 'guides/app-sync', label: 'App Sync (*arr / qui)'},
        {type: 'doc', id: 'guides/smoke-test', label: 'Golden smoke test'},
      ],
    },
    {type: 'doc', id: 'configuration', label: 'Configuration'},
    {type: 'doc', id: 'api', label: 'API & Swagger UI'},
    {
      type: 'category',
      label: 'Features',
      link: {type: 'doc', id: 'features/overview'},
      items: [
        {type: 'doc', id: 'features/overview', label: 'All features'},
        {
          type: 'doc',
          id: 'features/search-results-cache',
          label: 'Search-results cache',
        },
        {
          type: 'doc',
          id: 'features/circuit-breaker',
          label: 'Failing-tracker circuit breaker',
        },
        {
          type: 'doc',
          id: 'features/usenet-newznab',
          label: 'Usenet (Newznab) indexers',
        },
        {
          type: 'doc',
          id: 'features/cross-seed-freeleech',
          label: 'Cross-seed & freeleech',
        },
        {
          type: 'doc',
          id: 'features/download-clients',
          label: 'Download clients',
        },
        {type: 'doc', id: 'features/pagination', label: 'Pagination'},
        {
          type: 'doc',
          id: 'features/vip-expiry',
          label: 'VIP & membership expiry',
        },
      ],
    },
  ],
};

export default sidebars;
