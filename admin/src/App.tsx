// HomeCube Admin · P1 占位页
// 简单布局，预留后续功能入口

export default function App() {
  return (
    <div style={{ 
      minHeight: '100vh', 
      display: 'flex', 
      flexDirection: 'column',
      alignItems: 'center', 
      justifyContent: 'center',
      fontFamily: 'system-ui, -apple-system, sans-serif'
    }}>
      <h1 style={{ fontSize: '32px', marginBottom: '16px' }}>HomeCube Admin</h1>
      <p style={{ fontSize: '18px', color: '#666' }}>P1 占位</p>
      <div style={{ marginTop: '48px', padding: '24px', border: '1px solid #ddd', borderRadius: '8px' }}>
        <h2 style={{ fontSize: '20px', marginBottom: '12px' }}>预留功能入口</h2>
        <ul style={{ listStyle: 'none', padding: 0 }}>
          <li style={{ marginBottom: '8px' }}>• 用户管理（待实现）</li>
          <li style={{ marginBottom: '8px' }}>• 财务管理（待实现）</li>
          <li style={{ marginBottom: '8px' }}>• 系统配置（待实现）</li>
        </ul>
      </div>
    </div>
  )
}
