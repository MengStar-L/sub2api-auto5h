import { mount } from '@vue/test-utils'
import StatusBadge from './StatusBadge.vue'

describe('StatusBadge', () => {
  it('renders a translated verified state', () => {
    const wrapper = mount(StatusBadge, { props: { state: 'verified' } })
    expect(wrapper.text()).toContain('已验证')
    expect(wrapper.classes()).toContain('success')
  })

  it('renders unknown states without hiding them', () => {
    const wrapper = mount(StatusBadge, { props: { state: 'future_state' } })
    expect(wrapper.text()).toContain('future_state')
  })
})
