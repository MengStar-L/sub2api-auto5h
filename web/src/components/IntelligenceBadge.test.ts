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
})
