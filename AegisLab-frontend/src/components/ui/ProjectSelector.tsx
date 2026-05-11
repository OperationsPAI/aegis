import { useMemo, useState } from 'react';
import { FolderOutlined, SearchOutlined } from '@ant-design/icons';

import { DropdownMenu, type DropdownItem } from './DropdownMenu';
import './ProjectSelector.css';

export interface ProjectOption {
  id: string;
  name: string;
}

interface ProjectSelectorProps {
  projects: ProjectOption[];
  selectedId?: string;
  onSelect: (id: string) => void;
  placeholder?: string;
  className?: string;
  align?: 'left' | 'right';
}

export function ProjectSelector({
  projects,
  selectedId,
  onSelect,
  placeholder = 'Select a project',
  className,
  align = 'left',
}: ProjectSelectorProps) {
  const [search, setSearch] = useState('');

  const selected = useMemo(
    () => projects.find((p) => p.id === selectedId),
    [projects, selectedId],
  );

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return projects;
    return projects.filter((p) => p.name.toLowerCase().includes(q));
  }, [projects, search]);

  const dropdownItems: DropdownItem[] = useMemo(() => {
    const items: DropdownItem[] = [
      {
        key: '__search',
        label: (
          <div className="aegis-project-selector__search">
            <SearchOutlined />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onClick={(e) => e.stopPropagation()}
              placeholder="Search projects…"
              className="aegis-project-selector__search-input"
            />
          </div>
        ),
        onClick: () => { /* search input handles its own events */ },
        disabled: true,
      },
    ];

    if (filtered.length === 0) {
      items.push({
        key: '__empty',
        label: 'No projects found',
        disabled: true,
      });
    } else {
      filtered.forEach((p) => {
        items.push({
          key: p.id,
          label: (
            <span
              className={
                p.id === selectedId
                  ? 'aegis-project-selector__name aegis-project-selector__name--active'
                  : 'aegis-project-selector__name'
              }
            >
              {p.name}
            </span>
          ),
          icon: <FolderOutlined />,
          onClick: () => {
            onSelect(p.id);
            setSearch('');
          },
        });
      });
    }

    return items;
  }, [filtered, search, selectedId, onSelect]);

  const trigger = (
    <div className="aegis-project-selector__trigger">
      <FolderOutlined className="aegis-project-selector__trigger-icon" />
      <span className="aegis-project-selector__trigger-label">
        {selected?.name ?? placeholder}
      </span>
      {selected && (
        <span className="aegis-project-selector__trigger-chevron">▼</span>
      )}
    </div>
  );

  const cls = [
    'aegis-project-selector',
    selected ? '' : 'aegis-project-selector--empty',
    className ?? '',
  ].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <DropdownMenu trigger={trigger} items={dropdownItems} align={align} />
    </div>
  );
}

export default ProjectSelector;
