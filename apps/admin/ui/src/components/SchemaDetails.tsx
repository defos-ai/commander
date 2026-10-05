import { useTranslation } from '../i18n';
import '../styles/missions.css';

function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

export function SchemaDetails({ schema, direction }: { schema: Record<string, unknown> | null; direction: 'input' | 'output' }) {
  const { t } = useTranslation();
  const properties = Object.entries(object(schema?.properties) ?? {});
  const required = Array.isArray(schema?.required) ? schema.required : [];
  return <section className="mission-section">
    <h2>{t($ => $.missions[direction])}</h2>
    {!schema ? <p className="mission-muted">{t($ => $.missions.noSchema)}</p> : <>
      {properties.length > 0 && <div className="mission-fields-scroll" tabIndex={0} role="region" aria-label={t($ => $.missions[direction])}>
        <table className="mission-fields">
          <thead><tr><th>{t($ => $.missions.field)}</th><th>{t($ => $.missions.type)}</th><th>{t($ => $.missions.descriptionLabel)}</th></tr></thead>
          <tbody>{properties.map(([name, value]) => {
            const field = object(value);
            const type = typeof field?.type === 'string' ? field.type
              : Array.isArray(field?.type) && field.type.every(item => typeof item === 'string') ? field.type.join(' | ')
                : t($ => $.missions.schemaType);
            return <tr key={name}>
              <th scope="row"><code>{name}</code><span className="mission-field-requirement">{required.includes(name) ? t($ => $.missions.required) : t($ => $.missions.optional)}</span></th>
              <td><code>{type}</code></td>
              <td>{typeof field?.description === 'string' && <p>{field.description}</p>}
                {field && 'default' in field && <p className="mission-muted">{t($ => $.missions.defaultValue, { value: JSON.stringify(field.default) })}</p>}
              </td>
            </tr>;
          })}</tbody>
        </table>
      </div>}
      <details className="mission-schema"><summary>{direction === 'input' ? t($ => $.missions.inputSchema) : t($ => $.missions.outputSchema)}</summary>
        <pre>{JSON.stringify(schema, null, 2)}</pre>
      </details>
    </>}
  </section>;
}
