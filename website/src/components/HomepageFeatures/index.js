import clsx from 'clsx';
import Link from '@docusaurus/Link';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

const FeatureList = [
  {
    title: 'Cardigann parity',
    description:
      "Behavioral parity with Jackett's Cardigann engine — the same tracker " +
      'definitions, byte-for-byte, on a single binary.',
  },
  {
    title: 'Single static binary',
    description:
      'No runtime, no dependencies to install. One binary or one container, ' +
      'and you have a running provider.',
    to: '/docs/getting-started',
  },
  {
    title: 'Torznab + Newznab serving',
    description:
      'Serves torrents and Usenet indexers side by side through the same ' +
      'standard feed your apps already speak.',
    to: '/docs/features/usenet-newznab',
  },
  {
    title: 'App sync',
    description:
      'Push indexer config straight into Sonarr, Radarr, and qui — configure ' +
      'a tracker once, use it everywhere.',
    to: '/docs/guides/app-sync',
  },
  {
    title: 'Search-results cache',
    description:
      'A shared cache spares your trackers from duplicate requests every app ' +
      'would otherwise make on its own.',
    to: '/docs/features/search-results-cache',
  },
  {
    title: 'Freeleech & cross-seed',
    description:
      'One tracker serves both ratio-building *arrs and cross-seed, with a ' +
      'per-indexer freeleech toggle.',
    to: '/docs/features/cross-seed-freeleech',
  },
];

function Feature({title, description, to}) {
  const content = (
    <>
      <Heading as="h3">{title}</Heading>
      <p>{description}</p>
    </>
  );
  return (
    <div className={clsx('col col--4', styles.featureCol)}>
      {to ? (
        <Link to={to} className={styles.featureCard}>
          {content}
        </Link>
      ) : (
        <div className={styles.featureCard}>{content}</div>
      )}
    </div>
  );
}

export default function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
