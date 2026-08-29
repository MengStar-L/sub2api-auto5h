import { mount } from '@vue/test-utils'
import IntelligenceBadge from './IntelligenceBadge.vue'

describe('IntelligenceBadge', () => {
  it('renders a normal result', () => {
    const wrapper = mount(IntelligenceBadge, { props: { status: 'normal' } })
    expect(wrapper.text()).toContain('智商正常')
    expect(wrapper.classes()).toContain('success')
  })

  it('renders an abnormal result', () => {
    const wrapper = mount(IntelligenceBadge, { props: { status: 'abnormal' } })
    expect(wrapper.text()).toContain('智商不正常')
    expect(wrapper.classes()).toContain('danger')
  })

  it('renders an untested result', () => {
    const wrapper = mount(IntelligenceBadge, { props: { status: '' } })
    expect(wrapper.text()).toContain('未测试')
  })

  it('distinguishes empty and invalid legacy results', () => {
    expect(mount(IntelligenceBadge, { props: { status: 'no_answer' } }).text()).toContain('无有效回答')
    expect(mount(IntelligenceBadge, { props: { status: 'legacy_invalid' } }).text()).toContain('旧版结果无效')
  })
})
