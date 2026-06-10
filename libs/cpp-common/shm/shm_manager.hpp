#pragma once

#include <string>
#include <stdexcept>
#include <sys/mman.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <iostream>

namespace trading {
namespace shm {

template <typename T>
class ShmManager {
public:
    ShmManager(const std::string& name, bool read_only = true) 
        : m_name("/" + name), m_size(sizeof(T)), m_read_only(read_only), m_ptr(nullptr), m_fd(-1) {}

    ~ShmManager() {
        if (m_ptr && m_ptr != MAP_FAILED) {
            munmap(m_ptr, m_size);
        }
        if (m_fd != -1) {
            close(m_fd);
        }
    }

    bool init() {
        int oflag = m_read_only ? O_RDONLY : (O_CREAT | O_RDWR);
        mode_t mode = 0666;

        m_fd = shm_open(m_name.c_str(), oflag, mode);
        if (m_fd == -1) {
            std::cerr << "[ShmManager] shm_open failed for " << m_name << "\n";
            return false;
        }

        if (!m_read_only) {
            if (ftruncate(m_fd, m_size) == -1) {
                std::cerr << "[ShmManager] ftruncate failed for " << m_name << "\n";
                return false;
            }
        }

        int prot = m_read_only ? PROT_READ : (PROT_READ | PROT_WRITE);
        m_ptr = mmap(0, m_size, prot, MAP_SHARED, m_fd, 0);

        if (m_ptr == MAP_FAILED) {
            std::cerr << "[ShmManager] mmap failed for " << m_name << "\n";
            return false;
        }

        return true;
    }

    T* get() const {
        return static_cast<T*>(m_ptr);
    }

private:
    std::string m_name;
    size_t m_size;
    bool m_read_only;
    void* m_ptr;
    int m_fd;
};

} // namespace shm
} // namespace trading