import { useNavigate } from 'react-router-dom';

import { Menu } from 'antd';

interface ProjectSubNavProps {
  projectId: number;
  activeKey: string;
}

/**
 * Horizontal sub-navigation bar for project pages.
 * Appears below the breadcrumb on every project sub-page.
 */
const ProjectSubNav: React.FC<ProjectSubNavProps> = ({
  projectId,
  activeKey,
}) => {
  const navigate = useNavigate();

  const items = [
    { key: 'overview', label: 'Overview' },
    { key: 'datapacks', label: 'Datapacks' },
    { key: 'executions', label: 'Executions' },
    { key: 'evaluations', label: 'Evaluations' },
    { key: 'settings', label: 'Settings' },
  ];

  const handleClick = ({ key }: { key: string }) => {
    if (key === 'overview') {
      navigate(`/projects/${projectId}`);
    } else {
      navigate(`/projects/${projectId}/${key}`);
    }
  };

  return (
    <Menu
      mode='horizontal'
      selectedKeys={[activeKey]}
      items={items}
      onClick={handleClick}
      style={{ marginBottom: 24, borderBottom: '1px solid #f0f0f0' }}
    />
  );
};

export default ProjectSubNav;
